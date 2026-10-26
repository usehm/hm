package app

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/workspace"
)

func (a *App) openSettings() { a.ws.OpenPage(workspace.KindSettings) }

var settingsScopes = []ui.SegmentItem{
	{Label: "User", Value: "user"},
	{Label: "Project", Value: "project"},
}

// settingsPage is the Settings tab: a form over the merged config. Changes
// are written to the user or project YAML file, keeping its comments.
func (a *App) settingsPage(c *ui.Ctx) ui.View {
	th := c.Theme()
	cfg := a.cfg.Current()
	ed := cfg.Editor
	user, project := a.cfg.Paths()
	target := user
	if a.settingsProject {
		target = project
	}
	scope := 0
	if a.settingsProject {
		scope = 1
	}

	header := ui.Row(
		ui.Title("Settings"),
		ui.Spacer(),
		ui.Segmented("settings-scope", settingsScopes...).Selected(scope).
			OnSelectItem(func(i int, _ string) { a.settingsProject = i == 1 }),
		ui.Button("settings-yaml", ui.Text("Edit as YAML")).IconStart(icons.FileCode).
			Tooltip(target).OnClick(func() { a.openConfigYAML(a.settingsProject) }),
		ui.Button("settings-defaults", ui.Text("Defaults")).Subtle().IconStart(icons.FileText).OnClick(a.openDefaults),
	).Gap(th.Spacing.S)

	var alerts []ui.View
	if ps := a.cfg.Problems(); len(ps) > 0 {
		msg := ps[0].Error()
		if len(ps) > 1 {
			msg += " (+" + strconv.Itoa(len(ps)-1) + " more)"
		}
		alerts = append(alerts, ui.Alert("Settings were not applied: "+msg, ui.AlertError))
	}

	key := func(k ...string) []string { return k }
	form := ui.Form("settings-form",
		ui.FormHeading("Appearance", "Writes to "+a.ws.Rel(target)),
		ui.FormSelect("set-theme", "Theme", "Color theme for the whole window",
			themeOptions(), themeIndex(ed.Theme), func(v string) { a.setSetting(key("editor", "theme"), v) }),
		ui.FormNumber("set-font-size", "Font size", "Code font size in points",
			float64(ed.FontSize), 6, 72, 1, func(v float64) { a.setSetting(key("editor", "font_size"), int(v)) }),
		ui.FormText("set-font-family", "Font family", "Monospace font for code; empty uses the built-in one",
			a.textValue(key("editor", "font_family"), ed.FontFamily), func(v string) { a.setTextSetting(key("editor", "font_family"), v) }),

		ui.FormHeading("Editor", ""),
		ui.FormNumber("set-tab-width", "Tab width", "Columns per tab stop",
			float64(ed.TabWidth), 1, 16, 1, func(v float64) { a.setSetting(key("editor", "tab_width"), int(v)) }),
		ui.FormSwitch("set-spaces", "Insert spaces", "Tab inserts spaces instead of a tab",
			ed.InsertSpaces, func(v bool) { a.setSetting(key("editor", "insert_spaces"), v) }),
		ui.FormSwitch("set-wrap", "Soft wrap", "Wrap long lines at the window edge",
			ed.SoftWrap, func(v bool) { a.setSetting(key("editor", "soft_wrap"), v) }),
		ui.FormSwitch("set-lines", "Line numbers", "Show the line-number gutter",
			ed.LineNumbers, func(v bool) { a.setSetting(key("editor", "line_numbers"), v) }),
		ui.FormSwitch("set-format", "Format on save", "Run the language's formatters when saving",
			ed.FormatOnSave, func(v bool) { a.setSetting(key("editor", "format_on_save"), v) }),
		ui.FormSelect("set-autosave", "Auto save", "Save files without asking",
			enumOptions("editor.auto_save"), enumIndex("editor.auto_save", ed.AutoSave),
			func(v string) { a.setSetting(key("editor", "auto_save"), v) }),
		ui.FormSwitch("set-hidden", "Show hidden files", "List dotfiles in the explorer",
			ed.ShowHidden, func(v bool) { a.setSetting(key("editor", "show_hidden"), v) }),

		ui.FormHeading("Search", ""),
		ui.FormSelect("set-engine", "Engine", "ripgrep when installed, else the built-in search",
			enumOptions("search.engine"), enumIndex("search.engine", cfg.Search.Engine),
			func(v string) { a.setSetting(key("search", "engine"), v) }),
		ui.FormText("set-exclude", "Exclude", "Folders and globs to skip, comma separated",
			a.textValue(key("search", "exclude"), strings.Join(cfg.Search.Exclude, ", ")),
			func(v string) { a.setListSetting(key("search", "exclude"), v) }),

		ui.FormHeading("Terminal", ""),
		ui.FormText("set-shell", "Shell", `"auto" uses $SHELL`,
			a.textValue(key("terminal", "shell"), cfg.Terminal.Shell), func(v string) { a.setTextSetting(key("terminal", "shell"), v) }),
		ui.FormNumber("set-term-size", "Font size", "Terminal font size in points",
			float64(cfg.Terminal.FontSize), 6, 72, 1, func(v float64) { a.setSetting(key("terminal", "font_size"), int(v)) }),

		ui.FormHeading("Updates", ""),
		ui.FormSelect("set-channel", "Channel", "Which releases to offer",
			enumOptions("updates.channel"), enumIndex("updates.channel", cfg.Updates.Channel),
			func(v string) { a.setSetting(key("updates", "channel"), v) }),
	).Grouped()

	body := append([]ui.View{header}, alerts...)
	body = append(body, form, a.agentSettings(c),
		ui.Caption("Keyboard shortcuts, languages, provider details, modes and tools are edited in YAML."))
	return ui.Scroll("settings-scroll",
		ui.Column(body...).Gap(th.Spacing.M).Padding(th.Spacing.XL).Width(760),
	).Grow(1)
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
	).Grouped()}
	for _, name := range provs {
		name := name
		pc := cfg.Providers[name]
		status, keyLabel := "No key: add one to choose a model", "Add key"
		if a.hasProviderKey(name) {
			status, keyLabel = "Key set", "Replace key"
		} else if pc.Type == "openai" && localEndpoint(pc.BaseURL) {
			status = "Local endpoint, no key needed"
		}
		items := []ui.FormItem{ui.FormHeading(providerLabel(name), status)}
		if pc.Type == "openai" {
			base := []string{"agent", "providers", name, "base_url"}
			items = append(items, ui.FormText("set-agent-base-"+name, "Base URL",
				"OpenAI, OpenRouter, Ollama, LM Studio, Groq, DeepSeek, …",
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
