package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/workspace"
)

func (a *App) titleBar(c *ui.Ctx) ui.View {
	sideIcon := icons.PanelLeft
	if a.sideOpen {
		sideIcon = icons.PanelLeftClose
	}
	agentIcon := icons.PanelRight
	if a.agentOpen {
		agentIcon = icons.PanelRightClose
	}
	return ui.TitleBar(
		ui.IconButton("tb-side", sideIcon).Tooltip("Toggle sidebar").OnClick(a.toggleSide),
		ui.Button("tb-folder", ui.Text(filepath.Base(a.ws.Root))).Ghost().HoverFill().
			IconStart(icons.FolderOpen).Tooltip(a.ws.Root).OnClick(a.askOpenFolder),
		ui.Spacer(),
		ui.Button("tb-palette", ui.Text("Search commands…")).Width(320).
			IconStart(icons.Search).
			Hint(c.Commands().ToggleLabel()).
			OnClick(func() { c.Commands().Show() }),
		ui.Spacer(),
		ui.IconButton("tb-settings", icons.Settings).Tooltip("Settings").OnClick(a.openSettings),
		ui.IconButton("tb-agent", agentIcon).Tooltip("Agent").OnClick(a.toggleAgent),
	)
}

func (a *App) toggleSide()  { a.sideOpen = !a.sideOpen }
func (a *App) toggleAgent() { a.agentOpen = !a.agentOpen }

// center is everything between the title bar and the footer: the explorer,
// the tabs and editor, the bottom panel and the agent drawer.
func (a *App) center(c *ui.Ctx) ui.View {
	work := ui.Drawer("bottom-panel", a.panelView(c), a.editorArea(c)).
		Open(a.panelOpen).
		Edge(ui.EdgeBottom).
		Push().
		Size(220).
		Resizable(true).
		OnOpenChange(func(v bool) { a.panelOpen = v }).
		Grow(1)
	work = ui.Drawer("agent-panel", a.agentView(c), work).
		Open(a.agentOpen).
		Edge(ui.EdgeRight).
		Push().
		Size(380).
		Resizable(true).
		OnOpenChange(func(v bool) { a.agentOpen = v }).
		Grow(1)
	if !a.sideOpen {
		return work
	}
	return ui.Splitter("side-split", ui.Horizontal, a.explorer(c), work).
		Sizes(260, 0).MinSizes(160, 320).Grow(1)
}

func (a *App) explorer(c *ui.Ctx) ui.View {
	th := c.Theme()
	return ui.Column(
		ui.Row(
			ui.Caption(strings.ToUpper(filepath.Base(a.ws.Root))).Ellipsis(ui.EllipsisEnd).Grow(1),
			ui.IconButton("tree-new-file", icons.FilePlus).Tooltip("New file").OnClick(func() { a.newEntry(a.ws.Root, false) }),
			ui.IconButton("tree-new-folder", icons.FolderPlus).Tooltip("New folder").OnClick(func() { a.newEntry(a.ws.Root, true) }),
			ui.IconButton("tree-refresh", icons.RefreshCw).Tooltip("Refresh").OnClick(func() {
				a.tree.Reload(a.ws.Root)
				a.idx.refresh()
			}),
		).Gap(th.Spacing.XXS).Height(30).PaddingLeft(th.Spacing.M).PaddingRight(th.Spacing.XS),
		ui.TextField("tree-filter", a.treeQuery).
			Placeholder("Filter files").
			IconStart(icons.Search).
			OnChange(func(q string) {
				a.treeQuery = q
				a.tree.SetFilter(q)
			}).
			MarginXY(th.Spacing.S, th.Spacing.XS),
		ui.ViewOf(a.tree).Grow(1),
	).Grow(1).Background(ui.TokenChrome)
}

func (a *App) editorArea(c *ui.Ctx) ui.View {
	if len(a.ws.Tabs) == 0 {
		return a.welcome(c)
	}
	tabs := make([]ui.TabModel, len(a.ws.Tabs))
	for i, t := range a.ws.Tabs {
		tabs[i] = a.tabModel(t, i)
	}
	return ui.Column(
		ui.Tabs("editor-tabs", tabs).
			Selected(a.ws.Active).
			OnSelectItem(func(i int, _ string) { a.ws.Active = i }).
			OnTabClose(a.closeTab).
			OnTabContextMenu(a.tabMenu),
		a.activeView(c),
	).Grow(1)
}

func (a *App) tabModel(t workspace.Tab, i int) ui.TabModel {
	switch t.Kind {
	case workspace.KindSettings:
		return ui.TabModel{Title: "Settings", Icon: icons.Settings}
	}
	return ui.TabModel{Title: a.tabTitle(t.Path), Modified: a.ws.Modified(i), Icon: icons.File}
}

// tabTitle is the file name, with its folder when another tab has the same name.
func (a *App) tabTitle(path string) string {
	name := filepath.Base(path)
	for _, t := range a.ws.Tabs {
		if t.Path != path && t.Kind == workspace.KindFile && filepath.Base(t.Path) == name {
			return name + " — " + filepath.Base(filepath.Dir(path))
		}
	}
	return name
}

func (a *App) tabMenu(i int) []ui.MenuItem {
	t := a.ws.Tabs[i]
	items := []ui.MenuItem{
		{Label: "Close", OnSelect: func() { a.closeTab(i) }},
		{Label: "Close Others", OnSelect: func() { a.closeOthers(i) }},
	}
	if t.Kind == workspace.KindFile {
		items = append(items,
			ui.MenuItem{Label: "Copy Path", OnSelect: func() { a.copyText(t.Path) }},
			ui.MenuItem{Label: "Reveal in Explorer", OnSelect: func() {
				a.sideOpen = true
				a.tree.Reveal(t.Path)
			}},
		)
	}
	return items
}

// closeOthers closes every tab but i, keeping tabs with unsaved changes.
func (a *App) closeOthers(i int) {
	keep := a.ws.Tabs[i]
	for j := len(a.ws.Tabs) - 1; j >= 0; j-- {
		if a.ws.Tabs[j] != keep && !a.ws.Modified(j) {
			a.ws.Close(j)
		}
	}
	for j, t := range a.ws.Tabs {
		if t == keep {
			a.ws.Active = j
		}
	}
}

func (a *App) activeView(c *ui.Ctx) ui.View {
	t := a.ws.ActiveTab()
	switch t.Kind {
	case workspace.KindSettings:
		return a.settingsPage(c)
	}
	ed := a.ws.ActiveEditor()
	if ed == nil {
		return ui.EmptyState("File not open", t.Path).Grow(1)
	}
	view := ui.ViewOf(ed).Grow(1)
	if ed.HighlightOversize() {
		th := c.Theme()
		return ui.Column(
			ui.Row(
				ui.Caption("Highlighting is off for this large file."),
				ui.Spacer(),
				ui.Button("hl-anyway", ui.Text("Highlight anyway")).Subtle().OnClick(ed.HighlightAnyway),
			).PaddingXY(th.Spacing.M, th.Spacing.XS).Background(ui.TokenChromeMuted),
			view,
		).Grow(1)
	}
	return view
}

func (a *App) welcome(c *ui.Ctx) ui.View {
	th := c.Theme()
	return ui.Center(
		ui.Column(
			ui.Title("hm"),
			ui.Muted("Editor and coding agent"),
			ui.Row(
				ui.Button("welcome-open", ui.Text("Open Folder")).Primary().IconStart(icons.FolderOpen).OnClick(a.askOpenFolder),
				ui.Button("welcome-files", ui.Text("Go to File")).IconStart(icons.Search).Hint("⌘P").OnClick(a.showQuickOpen),
				ui.Button("welcome-palette", ui.Text("Commands")).IconStart(icons.Command).Hint(c.Commands().ToggleLabel()).
					OnClick(func() { c.Commands().Show() }),
			).Gap(th.Spacing.S),
		).Gap(th.Spacing.M).Align(ui.AlignCenter),
	).Grow(1)
}

func (a *App) footer(c *ui.Ctx) ui.View {
	th := c.Theme()
	items := []ui.View{}
	if br := gitBranch(a.ws.Root); br != "" {
		items = append(items, ui.Button("ft-branch", ui.Caption(br)).Ghost().HoverFill().
			IconStart(icons.GitBranch).Tooltip("Git").OnClick(func() { a.showPanel(panelGit) }))
	}
	errs, warns := countProblems(a.problems())
	items = append(items,
		ui.Button("ft-problems", ui.Row(
			ui.Icon(icons.CircleX, 14, th.ErrorForeground), ui.Caption(fmt.Sprint(errs)),
			ui.Icon(icons.TriangleAlert, 14, th.WarningForeground), ui.Caption(fmt.Sprint(warns)),
		).Gap(th.Spacing.XXS)).Ghost().HoverFill().Tooltip("Problems").
			OnClick(func() { a.togglePanel(panelProblems) }),
		ui.Spacer(),
	)
	if len(a.formatting) > 0 {
		items = append(items, ui.Spinner("ft-formatting", 12), ui.Caption("Formatting…"))
	}
	if ed := a.ws.ActiveEditor(); ed != nil {
		line, col := ed.LineCol(ed.Caret())
		items = append(items,
			ui.Caption(languageName(ed.Path)),
			a.serverStatus(c, ed.Path),
			ui.Caption(fmt.Sprintf("Ln %d, Col %d", line+1, col+1)).MarginXY(th.Spacing.S, 0),
		)
	}
	unread := ""
	if n := a.notifs.Unread(); n > 0 {
		unread = fmt.Sprint(n)
	}
	items = append(items,
		ui.Button("ft-terminal", ui.Caption("Terminal")).Ghost().HoverFill().
			IconStart(icons.SquareTerminal).OnClick(func() { a.togglePanel(panelTerminal) }),
		ui.Button("ft-git", ui.Caption("Git")).Ghost().HoverFill().
			IconStart(icons.GitCompare).OnClick(func() { a.togglePanel(panelGit) }),
		ui.Popover("ft-notifs-pop",
			ui.Button("ft-notifs", ui.Caption(unread)).Ghost().HoverFill().IconStart(icons.Bell).Tooltip("Notifications"),
			a.notifs.Layout(c),
		).
			Open(a.notifsOpen).
			OnOpenChange(func(v bool) {
				a.notifsOpen = v
				if v {
					a.notifs.MarkRead()
				}
			}).
			Placement(ui.PlacementTop).
			Width(380).
			Height(300),
	)
	return ui.Row(items...).Gap(th.Spacing.XS).PaddingXY(th.Spacing.S, 0).Height(26).
		Background(ui.TokenChrome).BorderTop(ui.TokenBorder, th.Stroke.Thin)
}

// serverStatus shows the language server state for path.
func (a *App) serverStatus(c *ui.Ctx, path string) ui.View {
	if !a.lang.HasServer(path) {
		return nil
	}
	th := c.Theme()
	cfg, _ := lsp.ForPath(path)
	icon, color, tip := icons.CircleDashed, th.ForegroundMuted, cfg.Command+" starting"
	if a.lang.Running(path) {
		icon, color, tip = icons.CircleCheck, th.SuccessForeground, cfg.Command+" running"
	}
	return ui.Button("ft-lsp", ui.Caption(cfg.Command)).Ghost().HoverFill().
		Tooltip(tip + " · click to restart").
		IconStart(icon).
		OnClick(func() { a.lang.Restart(); a.toast("Language servers restarted", ui.ToastInfo) }).
		Style(ui.Spec{}.TextColorLit(color))
}

// gitBranch reads the current branch from .git/HEAD; the git package replaces
// this with live status.
func gitBranch(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		return ref
	}
	if len(head) >= 7 {
		return head[:7]
	}
	return head
}

var languageNames = map[string]string{
	".go": "Go", ".mod": "Go Module", ".json": "JSON", ".yaml": "YAML", ".yml": "YAML",
	".md": "Markdown", ".py": "Python", ".ts": "TypeScript", ".tsx": "TypeScript React",
	".js": "JavaScript", ".jsx": "JavaScript React", ".rs": "Rust", ".sh": "Shell",
	".sql": "SQL", ".css": "CSS", ".html": "HTML", ".xml": "XML", ".toml": "TOML",
}

func languageName(path string) string {
	if n, ok := languageNames[strings.ToLower(filepath.Ext(path))]; ok {
		return n
	}
	return "Plain Text"
}
