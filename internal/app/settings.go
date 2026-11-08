package app

import (
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/workspace"
)

func (a *App) openSettings() { a.ws.OpenPage(workspace.KindSettings) }

var settingsScopes = []ui.SegmentItem{
	{Label: "User", Value: "user"},
	{Label: "Project", Value: "project"},
}

// settingsCategories are the Settings tab's sections, in list order; a
// change of group draws a divider.
var settingsCategories = []struct {
	id, label string
	icon      icons.Icon
	group     int
}{
	{"appearance", "Appearance", icons.Palette, 0},
	{"editor", "Editor", icons.FileCode, 0},
	{"files", "Files & Explorer", icons.FolderTree, 0},
	{"search", "Search", icons.Search, 0},
	{"terminal", "Terminal", icons.SquareTerminal, 0},
	{"agent", "Agent", icons.Bot, 1},
	{"completion", "Completion", icons.Sparkles, 1},
	{"security", "Agent Security", icons.ShieldCheck, 1},
	{"artifacts", "Artifacts", icons.Package, 1},
	{"keys", "Keyboard Shortcuts", icons.Keyboard, 2},
	{"updates", "Updates", icons.Download, 2},
}

// settingsPage is the Settings tab: the categories listed on the left, the
// selected category's form scrolling on the right. Changes are written to
// the user or project YAML file, keeping its comments.
func (a *App) settingsPage(c *ui.Ctx) ui.View {
	return a.settingsPageOf(c, nil, &a.settingsCat)
}

// settingsPageOf is the settings page limited to the categories in only
// (every one when nil), the selected one kept in *sel.
func (a *App) settingsPageOf(c *ui.Ctx, only []string, sel *string) ui.View {
	id := "settings-page"
	if only != nil {
		id = "settings-page-agent"
	}
	return ui.Responsive(id, func(w float32) ui.View {
		return a.settingsLayout(c, only, sel, w > 0 && w < settingsCompactW)
	}).Grow(1)
}

// settingsCompactW is the page width below which the category list folds
// into a menu on the title, leaving the form the whole width.
const settingsCompactW = 680

func (a *App) settingsLayout(c *ui.Ctx, only []string, sel *string, compact bool) ui.View {
	th := c.Theme()
	user, project := a.cfg.Paths()
	target := user
	if a.settingsProject {
		target = project
	}
	cat := *sel
	if cat == "" || only != nil && !slices.Contains(only, cat) {
		cat = settingsCategories[0].id
		if only != nil {
			cat = only[0]
		}
	}
	scope := 0
	if a.settingsProject {
		scope = 1
	}

	title := ""
	for _, sc := range settingsCategories {
		if sc.id == cat {
			title = sc.label
		}
	}
	var heading ui.View = ui.Title(title)
	if compact {
		heading = a.settingsCatMenu(c, cat, only, sel)
	}
	body := []ui.View{ui.Row(
		ui.Column(
			heading,
			ui.Row(ui.Caption("Writes to "+a.ws.Rel(target)).Ellipsis(ui.EllipsisMiddle).Shrink(1)),
		).Gap(th.Spacing.XXS).Grow(1).Shrink(1),
		ui.Segmented("settings-scope", settingsScopes...).Selected(scope).
			Tooltip("Which config file changes are saved to").
			OnSelectItem(func(i int, _ string) { a.settingsProject = i == 1 }),
	).Gap(th.Spacing.M).Align(ui.AlignCenter)}
	if ps := a.cfg.Problems(); len(ps) > 0 {
		msg := ps[0].Error()
		if len(ps) > 1 {
			msg += " (+" + strconv.Itoa(len(ps)-1) + " more)"
		}
		body = append(body, ui.Alert("Settings were not applied: "+msg, ui.AlertError))
	}
	body = append(body, a.settingsSection(c, cat))
	pad := th.Spacing.XL
	if compact {
		pad = th.Spacing.M
	}
	content := ui.Scroll("settings-scroll",
		ui.Column(body...).Gap(th.Spacing.L).PaddingXY(pad, pad).MaxWidth(800),
	).Grow(1)
	if compact {
		return ui.Column(content).Grow(1)
	}
	return ui.Row(a.settingsNav(c, cat, target, only, sel), content).Align(ui.AlignStretch).Grow(1)
}

// settingsCatMenu is the page title as a menu of the categories and the
// YAML links, standing in for the side list on a narrow page.
func (a *App) settingsCatMenu(c *ui.Ctx, cat string, only []string, sel *string) ui.View {
	var items []ui.MenuItem
	label := ""
	group := -1
	for _, sc := range settingsCategories {
		if only != nil && !slices.Contains(only, sc.id) {
			continue
		}
		if group >= 0 && sc.group != group {
			items = append(items, ui.MenuSeparator)
		}
		group = sc.group
		id := sc.id
		if id == cat {
			label = sc.label
		}
		items = append(items, ui.MenuItem{Label: sc.label, Icon: sc.icon, Checked: id == cat, OnSelect: func() { *sel = id }})
	}
	items = append(items, ui.MenuSeparator,
		ui.MenuItem{Label: "Edit as YAML", Icon: icons.FileCode, OnSelect: func() { a.openConfigYAML(a.settingsProject) }},
		ui.MenuItem{Label: "View defaults", Icon: icons.FileText, OnSelect: a.openDefaults},
	)
	return ui.Row(ui.MenuButton("settings-cat-menu", label, items).Ghost().Shrink(1))
}

// settingsNav lists the categories in groups, then links to the YAML files
// behind them.
func (a *App) settingsNav(c *ui.Ctx, cat, target string, only []string, sel *string) ui.View {
	th := c.Theme()
	divider := func() ui.View {
		return ui.Row().BorderTop(ui.TokenBorder, th.Stroke.Thin).MarginXY(th.Spacing.S, th.Spacing.S)
	}
	item := func(id, label string, icon icons.Icon) *ui.Node {
		return ui.Button(id, ui.Text(label)).Ghost().HoverFill().IconStart(icon).Justify(ui.JustifyStart)
	}
	// link opens a file in a new tab, marked like a link out of the page.
	link := func(id, label string, icon icons.Icon) *ui.Node {
		return ui.Button(id, ui.Row(ui.Text(label), ui.Icon(icons.ArrowUpRight, th.Metrics.IconSizeSM, th.ForegroundMuted)).
			Gap(th.Spacing.XS).Align(ui.AlignCenter)).Ghost().HoverFill().IconStart(icon).Justify(ui.JustifyStart)
	}
	var rows []ui.View
	group := -1
	for _, sc := range settingsCategories {
		if only != nil && !slices.Contains(only, sc.id) {
			continue
		}
		if group >= 0 && sc.group != group {
			rows = append(rows, divider())
		}
		group = sc.group
		id := sc.id
		b := item("settings-cat-"+id, sc.label, sc.icon).OnClick(func() { *sel = id })
		if id == cat {
			b = b.Background(ui.TokenListActive)
		}
		rows = append(rows, b)
	}
	rows = append(rows,
		divider(),
		link("settings-yaml", "Edit as YAML", icons.FileCode).
			Tooltip("Open "+a.ws.Rel(target)+": languages, providers, modes and tools are edited there").
			OnClick(func() { a.openConfigYAML(a.settingsProject) }),
		link("settings-defaults", "View defaults", icons.FileText).
			Tooltip("The built-in config, read only").OnClick(a.openDefaults),
	)
	return ui.Column(rows...).Gap(th.Spacing.XXS).PaddingXY(th.Spacing.M, th.Spacing.XL).Width(240)
}

// settingsSection is one category's form.
func (a *App) settingsSection(c *ui.Ctx, cat string) ui.View {
	cfg := a.cfg.Current()
	ed := cfg.Editor
	key := func(k ...string) []string { return k }
	switch cat {
	case "keys":
		return a.keysSection(c)
	case "security":
		return a.securitySection(c)
	case "agent":
		return a.agentSettings(c)
	case "artifacts":
		art := cfg.Artifacts
		fields := []ui.FormItem{
			ui.FormSelect("set-art-storage", "Storage", "Where the agent keeps artifacts. Not wired up yet",
				enumOptions("artifacts.storage"), enumIndex("artifacts.storage", art.Storage),
				func(v string) { a.setSetting(key("artifacts", "storage"), v) }),
		}
		switch art.Storage {
		case "gcs", "s3":
			fields = append(fields,
				ui.FormText("set-art-bucket", "Bucket", "The bucket's name",
					a.textValue(key("artifacts", "bucket"), art.Bucket), func(v string) { a.setTextSetting(key("artifacts", "bucket"), v) }),
				ui.FormText("set-art-prefix", "Prefix", "Folder inside the bucket; empty for its root",
					a.textValue(key("artifacts", "prefix"), art.Prefix), func(v string) { a.setTextSetting(key("artifacts", "prefix"), v) }))
		case "git":
			fields = append(fields,
				ui.FormText("set-art-repo", "Repository", "Remote URL to push artifacts to",
					a.textValue(key("artifacts", "repo"), art.Repo), func(v string) { a.setTextSetting(key("artifacts", "repo"), v) }),
				ui.FormText("set-art-branch", "Branch", "Branch artifacts are committed on",
					a.textValue(key("artifacts", "branch"), art.Branch), func(v string) { a.setTextSetting(key("artifacts", "branch"), v) }))
		}
		return ui.Form("settings-artifacts", fields...).Grouped()
	case "completion":
		return a.completionSettings()
	case "editor":
		return a.editorSection(c)
	case "files":
		return ui.Form("settings-files",
			ui.FormHeading("Saving", ""),
			ui.FormSelect("set-autosave", "Auto save", "Save files without asking",
				enumOptions("editor.auto_save"), enumIndex("editor.auto_save", ed.AutoSave),
				func(v string) { a.setSetting(key("editor", "auto_save"), v) }),
			ui.FormSwitch("set-format", "Format on save", "Run the language's formatters when saving",
				ed.FormatOnSave, func(v bool) { a.setSetting(key("editor", "format_on_save"), v) }),
			ui.FormSwitch("set-trim", "Trim trailing whitespace", "Strip blanks at line ends when saving",
				ed.TrimTrailingWhitespace, func(v bool) { a.setSetting(key("editor", "trim_trailing_whitespace"), v) }),
			ui.FormHeading("Explorer", ""),
			ui.FormSwitch("set-hidden", "Show hidden files", "List dotfiles in the file tree",
				ed.ShowHidden, func(v bool) { a.setSetting(key("editor", "show_hidden"), v) }),
		).Grouped()
	case "search":
		return ui.Form("settings-search",
			ui.FormSelect("set-engine", "Engine", "ripgrep when installed, else the built-in search",
				enumOptions("search.engine"), enumIndex("search.engine", cfg.Search.Engine),
				func(v string) { a.setSetting(key("search", "engine"), v) }),
			ui.FormText("set-exclude", "Exclude", "Folders and globs to skip, comma separated",
				a.textValue(key("search", "exclude"), strings.Join(cfg.Search.Exclude, ", ")),
				func(v string) { a.setListSetting(key("search", "exclude"), v) }),
		).Grouped()
	case "terminal":
		return ui.Form("settings-terminal",
			ui.FormText("set-shell", "Shell", `"auto" uses $SHELL; applies to new terminals`,
				a.textValue(key("terminal", "shell"), cfg.Terminal.Shell), func(v string) { a.setTextSetting(key("terminal", "shell"), v) }),
			ui.FormNumber("set-term-size", "Font size", "Terminal font size in points",
				float64(cfg.Terminal.FontSize), 6, 72, 1, func(v float64) { a.setSetting(key("terminal", "font_size"), int(v)) }),
		).Grouped()
	case "updates":
		return ui.Form("settings-updates",
			ui.FormSelect("set-channel", "Channel", "Which releases to offer",
				enumOptions("updates.channel"), enumIndex("updates.channel", cfg.Updates.Channel),
				func(v string) { a.setSetting(key("updates", "channel"), v) }),
		).Grouped()
	}
	return ui.Form("settings-appearance",
		ui.FormSelect("set-theme", "Theme", "Color theme for the whole window",
			themeOptions(), themeIndex(ed.Theme), func(v string) { a.setSetting(key("editor", "theme"), v) }),
		ui.FormSelect("set-zoom", "Zoom", "Scales the whole window; cmd+= and cmd+- step through these",
			zoomOptions(), nearestZoomStep(ed.Zoom), func(v string) {
				if z, err := strconv.ParseFloat(v, 64); err == nil {
					a.setZoom(z)
				}
			}),
		ui.FormNumber("set-font-size", "Font size", "Code font size in points",
			float64(ed.FontSize), 6, 72, 1, func(v float64) { a.setSetting(key("editor", "font_size"), int(v)) }),
		ui.FormText("set-font-family", "Font family", "Monospace font for code; empty uses the built-in one",
			a.textValue(key("editor", "font_family"), ed.FontFamily), func(v string) { a.setTextSetting(key("editor", "font_family"), v) }),
	).Grouped()
}

func themeOptions() []ui.SelectOption {
	names := append([]string{"system"}, theme.Names()...)
	opts := make([]ui.SelectOption, len(names))
	for i, n := range names {
		opts[i] = ui.SelectOption{Label: n, Value: n}
	}
	return opts
}

func themeIndex(name string) int {
	for i, o := range themeOptions() {
		if o.Value == name {
			return i
		}
	}
	return 0
}

func enumOptions(path string) []ui.SelectOption {
	words := config.Enums[path]
	opts := make([]ui.SelectOption, len(words))
	for i, w := range words {
		opts[i] = ui.SelectOption{Label: w, Value: w}
	}
	return opts
}

func enumIndex(path, v string) int {
	for i, w := range config.Enums[path] {
		if w == v {
			return i
		}
	}
	return 0
}

// agentSettings picks the default provider and mode, and per provider the
// endpoint, the API key (kept in the OS keychain) and, once a key is set,
// the model.
func (a *App) agentSettings(c *ui.Ctx) ui.View {
	th := c.Theme()
	cfg := a.cfg.Current().Agent
	provs := a.providerNames()
	popts := make([]ui.SelectOption, len(provs))
	psel := 0
	for i, p := range provs {
		popts[i] = ui.SelectOption{Label: providerLabel(p), Value: p}
		if p == cfg.DefaultProvider {
			psel = i
		}
	}
	modes := agent.ModeNames(a.modes())
	mopts := make([]ui.SelectOption, len(modes))
	msel := 0
	for i, m := range modes {
		mopts[i] = ui.SelectOption{Label: m, Value: m}
		if m == cfg.DefaultMode {
			msel = i
		}
	}
	rows := []ui.View{ui.Form("agent-form",
		ui.FormHeading("Agent", "Providers and modes live under agent: and modes: in the YAML"),
		ui.FormSelect("set-agent-provider", "Default provider", "Used by new sessions", popts, psel,
			func(v string) { a.setSetting([]string{"agent", "default_provider"}, v) }),
		ui.FormSelect("set-agent-mode", "Default mode", "lazy, rubber-duck, auto, fullstack, or your own", mopts, msel,
			func(v string) { a.setSetting([]string{"agent", "default_mode"}, v) }),
		ui.FormSelect("set-agent-layout", "Layout", "Full screen, toggled with the editor, or a panel beside it",
			enumOptions("agent.layout"), enumIndex("agent.layout", cfg.Layout),
			func(v string) { a.setSetting([]string{"agent", "layout"}, v) }),
		a.addProviderSetting(),
	).Grouped()}
	for _, name := range provs {
		name := name
		pc := cfg.Providers[name]
		status, keyLabel := "No key: add one to choose a model", "Add key"
		if a.hasProviderKey(name) {
			status, keyLabel = "Key set", "Replace key"
		} else if localProvider(pc) {
			status = "Local endpoint, no key needed"
		}
		items := []ui.FormItem{ui.FormHeading(providerLabel(name), status)}
		if provider.OpenAICompatible(pc.Type) {
			base := []string{"agent", "providers", name, "base_url"}
			hint := "OpenAI, OpenRouter, Ollama, LM Studio, Groq, DeepSeek, …"
			if p, ok := provider.Presets[pc.Type]; ok {
				hint = "Empty uses " + p.BaseURL
			}
			items = append(items, ui.FormText("set-agent-base-"+name, "Base URL", hint,
				a.textValue(base, pc.BaseURL), func(v string) { a.setTextSetting(base, v) }))
		}
		if a.providerReady(name) {
			items = append(items, a.modelSetting(name))
		}
		rows = append(rows, ui.Column(
			ui.Form("agent-provider-form-"+name, items...).Grouped(),
			ui.Row(ui.Spacer(), ui.Button("agent-key-"+name, ui.Text(keyLabel)).IconStart(icons.KeyRound).
				OnClick(func() { a.askProviderKey(name) })),
		).Gap(th.Spacing.XS))
	}
	return ui.Column(rows...).Gap(th.Spacing.M)
}

// completionSettings turns AI inline completions on and picks the provider
// and model they use.
func (a *App) completionSettings() ui.View {
	cc := a.cfg.Current().Completion
	name := a.completionProvider()
	provs := a.providerNames()
	popts := make([]ui.SelectOption, len(provs))
	psel := 0
	for i, p := range provs {
		popts[i] = ui.SelectOption{Label: providerLabel(p), Value: p}
		if p == name {
			psel = i
		}
	}
	status := "Ghost text at the caret: Tab accepts, Esc dismisses"
	if !a.providerReady(name) {
		status = "Add a key for " + providerLabel(name) + " under Agent to choose a model"
	} else if cc.Model == "" {
		status = "Choose a small, fast model to turn completions on"
	}
	items := []ui.FormItem{
		ui.FormHeading("Completion", status),
		ui.FormSwitch("set-compl-on", "AI completions", "Suggest code as you type",
			cc.Enabled, func(v bool) { a.setSetting([]string{"completion", "enabled"}, v) }),
		ui.FormSelect("set-compl-provider", "Provider", "Uses that provider's endpoint and key", popts, psel,
			func(v string) { a.setSetting([]string{"completion", "provider"}, v) }),
	}
	if a.providerReady(name) {
		key := []string{"completion", "model"}
		items = append(items, a.modelPicker("set-compl-model", name, cc.Model, key,
			func(v string) { a.setSetting(key, v) }))
	}
	items = append(items, ui.FormNumber("set-compl-delay", "Delay", "Milliseconds of pause in typing before asking",
		float64(cc.Delay.Milliseconds()), 0, 5000, 50,
		func(v float64) { a.setSetting([]string{"completion", "delay"}, strconv.Itoa(int(v))+"ms") }))
	return ui.Form("completion-form", items...).Grouped()
}

// askProviderKey asks for an API key and keeps it in the keychain.
func (a *App) askProviderKey(name string) {
	key := ""
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title: "API key for " + name, Width: 460, FitContent: true,
		Body: func(c *ui.Ctx) ui.View {
			th := c.Theme()
			return ui.Column(
				ui.Paragraph("The key is stored in "+keychainName()+", not in the config file; the config refers to it as keychain:"+name+"."),
				ui.TextField("provider-key", key).Password(true).Placeholder("Paste the key").DefaultFocus().
					OnChange(func(v string) { key = v }),
			).Gap(th.Spacing.M).Padding(th.Spacing.L)
		},
		Actions: []ui.DialogAction{
			{Label: "Cancel"},
			{Label: "Save", Primary: true, OnClick: func() { a.saveProviderKey(name, key) }},
		},
	})
}

func keychainName() string {
	switch runtime.GOOS {
	case "darwin":
		return "the macOS Keychain"
	case "windows":
		return "Windows Credential Manager"
	}
	return "the system keyring"
}
