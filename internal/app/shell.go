// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/workspace"
)

func (a *App) titleBar(c *ui.Ctx) ui.View {
	sideIcon := icons.PanelLeft
	if a.sideOpen {
		sideIcon = icons.PanelLeftClose
	}
	agentIcon, agentTip := icons.PanelRight, "Agent"
	if a.agentOpen {
		agentIcon = icons.PanelRightClose
	}
	if a.agentFullscreen() {
		// The button switches views: to the agent, or back to the editor.
		agentIcon = icons.BotMessageSquare
		if a.agentOpen {
			agentIcon, agentTip = icons.SquareCode, "Editor"
		}
	}
	return ui.TitleBar(
		ui.IconButton("tb-side", sideIcon).Tooltip("Toggle sidebar").OnClick(a.toggleSide),
		ui.Button("tb-folder", ui.Text(filepath.Base(a.ws.Root))).Ghost().HoverFill().
			IconStart(icons.FolderOpen).Tooltip(a.ws.Root).OnClick(a.askOpenFolder),
		ui.Spacer(),
		ui.IconButton("tb-settings", icons.Settings).Tooltip("Settings").OnClick(a.openSettings),
		ui.IconButton("tb-agent", agentIcon).Tooltip(agentTip+" ("+a.key("agent.toggle")+")").OnClick(a.toggleAgent),
	).Center(
		ui.Button("tb-palette", ui.Text("Search commands…")).Width(320).
			IconStart(icons.Search).
			Hint(c.Commands().ToggleLabel()).
			OnClick(func() { c.Commands().Show() }),
	)
}

func (a *App) toggleSide() { a.sideOpen = !a.sideOpen }

// agentFullscreen reports whether the agent takes the editor's place
// (agent.layout: fullscreen) rather than docking beside it.
func (a *App) agentFullscreen() bool { return a.cfg.Current().Agent.Layout != "panel" }
func (a *App) toggleAgent()          { a.setAgentOpen(!a.agentOpen) }

// agentShowsAlone reports whether the agent is full screen, in the editor's
// place.
func (a *App) agentShowsAlone() bool { return a.agentOpen && a.agentFullscreen() }

// setAgentOpen shows or hides the agent panel; opening it focuses the
// message box.
func (a *App) setAgentOpen(v bool) {
	a.agentOpen = v
	if r := a.run(); r != nil {
		r.focusInput = v
	}
}

// center is everything between the title bar and the footer: the explorer,
// the tabs and editor, the bottom panel and the agent drawer.
func (a *App) center(c *ui.Ctx) ui.View {
	if a.agentFullscreen() && a.agentOpen {
		work := ui.Drawer("bottom-panel", a.panelView(c), a.agentFullView(c)).
			Open(a.panelOpen).
			Edge(ui.EdgeBottom).
			Push().
			Size(220).
			Resizable(true).
			OnOpenChange(func(v bool) { a.panelOpen = v }).
			Grow(1)
		if !a.sideOpen {
			return work
		}
		// Full screen, the agent's sidebar takes the explorer's place.
		return ui.Splitter("agent-side-split", ui.Horizontal, a.agentSidebar(c), work).
			Sizes(260, 0).MinSizes(180, 320).Grow(1)
	}
	work := ui.Drawer("bottom-panel", a.panelView(c), a.editorArea(c)).
		Open(a.panelOpen).
		Edge(ui.EdgeBottom).
		Push().
		Size(220).
		Resizable(true).
		OnOpenChange(func(v bool) { a.panelOpen = v }).
		Grow(1)
	work = ui.Drawer("agent-panel", a.agentView(c), work).
		Open(a.agentOpen && !a.agentFullscreen()).
		Edge(ui.EdgeRight).
		Push().
		Size(380).
		MinSize(280).
		Resizable(true).
		OnOpenChange(a.setAgentOpen).
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
			ui.IconButton("tree-collapse", icons.ChevronsDownUp).Tooltip("Collapse all").OnClick(a.tree.CollapseAll),
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
	return a.withGroups(c, a.firstGroup(c))
}

// firstGroup is the workspace's tabs and the active one's view.
func (a *App) firstGroup(c *ui.Ctx) ui.View {
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
	case workspace.KindGit:
		return ui.TabModel{Title: "Git", Icon: icons.GitBranch}
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
		{Label: "Close to the Right", Disabled: i == len(a.ws.Tabs)-1, OnSelect: func() { a.closeRight(i) }},
		{Label: "Close Saved", OnSelect: a.closeSaved},
		{Label: "Close All", OnSelect: a.closeAllTabs},
	}
	if t.Kind == workspace.KindFile {
		items = append(items,
			ui.MenuItem{Separator: true},
			ui.MenuItem{Label: "Split Right", OnSelect: func() { a.focusGroup = 0; a.ws.Active = i; a.splitRight() }},
			ui.MenuItem{Label: "Move to Other Group", OnSelect: func() { a.focusGroup = 0; a.ws.Active = i; a.moveToOtherGroup() }},
		)
	}
	if t.Kind == workspace.KindFile {
		items = append(items,
			ui.MenuItem{Separator: true},
			ui.MenuItem{Label: "Copy Path", OnSelect: func() { a.copyText(t.Path) }},
			ui.MenuItem{Label: "Copy Relative Path", OnSelect: func() { a.copyText(a.ws.Rel(t.Path)) }},
			ui.MenuItem{Label: "Reveal in Explorer", OnSelect: func() {
				a.sideOpen = true
				a.tree.Reveal(t.Path)
			}},
		)
	}
	return items
}

func (a *App) activeView(c *ui.Ctx) ui.View {
	t := a.ws.ActiveTab()
	switch t.Kind {
	case workspace.KindSettings:
		return a.settingsPage(c)
	case workspace.KindGit:
		return a.gitPage(c)
	}
	ed := a.ws.ActiveEditor()
	if ed == nil {
		return ui.EmptyState("File not open", t.Path).Grow(1)
	}
	if a.search.open && a.search.preview == ed.Path {
		// The search dialog shows this editor; one widget, one place.
		return ui.Spacer()
	}
	view := ui.ViewOf(ed).Grow(1)
	if ed.HighlightOversize() {
		th := c.Theme()
		view = ui.Column(
			ui.Row(
				ui.Caption("Highlighting is off for this large file."),
				ui.Spacer(),
				ui.Button("hl-anyway", ui.Text("Highlight anyway")).Subtle().OnClick(ed.HighlightAnyway),
			).PaddingXY(th.Spacing.M, th.Spacing.XS).Background(ui.TokenChromeMuted),
			view,
		).Grow(1)
	}
	var body ui.View = view
	var trailing []ui.View
	if isMarkdown(ed.Path) {
		body = a.markdownView(c, ed, view)
		trailing = append(trailing, a.markdownMode(ed.Path))
	}
	return ui.Column(a.breadcrumb(c, ed.Path, trailing...), body).Grow(1)
}

// breadcrumb shows path relative to the workspace root; a folder crumb
// reveals that folder in the explorer. Trailing views sit at the row's end.
func (a *App) breadcrumb(c *ui.Ctx, path string, trailing ...ui.View) ui.View {
	rel, err := filepath.Rel(a.ws.Root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = path
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	segs := make([]ui.BreadcrumbSegment, len(parts))
	dir := a.ws.Root
	for i, p := range parts {
		dir = filepath.Join(dir, p)
		target := dir
		segs[i] = ui.BreadcrumbSegment{Label: p, OnSelect: func() {
			a.sideOpen = true
			a.tree.Reveal(target)
		}}
	}
	th := c.Theme()
	crumbs := ui.Breadcrumb("editor-crumbs", segs...).Height(24).Grow(1)
	return ui.Row(append([]ui.View{crumbs}, trailing...)...).Height(24).
		PaddingRight(th.Spacing.XS).Background(ui.TokenSurface).BorderBottom(ui.TokenBorder, th.Stroke.Thin)
}

func (a *App) welcome(c *ui.Ctx) ui.View {
	th := c.Theme()
	center := func(v ui.View) ui.View { return ui.Row(v).Justify(ui.JustifyCenter) }
	// Centered by spacers rather than ui.Center, so beside the agent panel
	// the buttons wrap instead of running past the editor's edges.
	inner := ui.Column(
		center(ui.Title("hm")),
		center(ui.Muted("Editor and coding agent")),
		ui.Row(
			ui.Button("welcome-open", ui.Text("Open Folder")).Primary().IconStart(icons.FolderOpen).OnClick(a.askOpenFolder),
			ui.Button("welcome-files", ui.Text("Go to File")).IconStart(icons.Search).Hint("⌘P").OnClick(a.showQuickOpen),
			ui.Button("welcome-palette", ui.Text("Commands")).IconStart(icons.Command).Hint(c.Commands().ToggleLabel()).
				OnClick(func() { c.Commands().Show() }),
		).Gap(th.Spacing.S).Wrap().Justify(ui.JustifyCenter),
	).Gap(th.Spacing.M).Align(ui.AlignStretch).Shrink(1).PaddingXY(th.Spacing.M, 0)
	return ui.Column(ui.Row(ui.Spacer(), inner, ui.Spacer())).Justify(ui.JustifyCenter).Grow(1)
}

func (a *App) footer(c *ui.Ctx) ui.View {
	th := c.Theme()
	items := []ui.View{}
	if v := a.branchButton(c); v != nil {
		items = append(items, v)
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
	if p := c.Commands().Pending(); p != "" {
		items = append(items, ui.Caption("("+p+") was pressed. Waiting for the second key…"))
	}
	if a.agentFullscreen() && a.agentOpen {
		items = append(items, a.agentStatus(c)...)
	} else if ed := a.curEditor(); ed != nil {
		if st := a.vimState(); st != nil {
			items = append(items, ui.Caption(st.Status()).Style(ui.Spec{}.TextColor(ui.TokenAccent)))
		}
		line, col := ed.LineCol(ed.Caret())
		items = append(items,
			ui.Caption(languageName(a.curPath())),
			a.serverStatus(c, a.curPath()),
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
			IconStart(icons.GitCompare).OnClick(a.toggleGit),
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
			Height(420),
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

// branchButton shows the branch, how far it is from its upstream and how
// many files changed; it opens the Git panel.
func (a *App) branchButton(c *ui.Ctx) ui.View {
	st := a.gitst.status
	if st == nil {
		return nil
	}
	label := st.Branch
	if label == "" {
		label = "detached"
		if len(st.Head) >= 7 {
			label = st.Head[:7]
		}
	}
	if st.Ahead > 0 {
		label += fmt.Sprintf(" ↑%d", st.Ahead)
	}
	if st.Behind > 0 {
		label += fmt.Sprintf(" ↓%d", st.Behind)
	}
	if n := len(st.Changes); n > 0 {
		label += fmt.Sprintf(" · %d changed", n)
	}
	tip := "Git"
	if st.Upstream != "" {
		tip = "Tracking " + st.Upstream
	}
	return ui.Button("ft-branch", ui.Caption(label)).Ghost().HoverFill().
		IconStart(icons.GitBranch).Tooltip(tip).OnClick(a.openGit)
}

var languageNames = map[string]string{
	".go": "Go", ".mod": "Go Module", ".json": "JSON", ".yaml": "YAML", ".yml": "YAML",
	".md": "Markdown", ".py": "Python", ".ts": "TypeScript", ".tsx": "TypeScript React",
	".js": "JavaScript", ".jsx": "JavaScript React", ".rs": "Rust", ".sh": "Shell",
	".sql": "SQL", ".css": "CSS", ".html": "HTML", ".xml": "XML", ".toml": "TOML",
	".java": "Java", ".proto": "Protocol Buffers", ".pyi": "Python",
}

func languageName(path string) string {
	if n, ok := languageNames[strings.ToLower(filepath.Ext(path))]; ok {
		return n
	}
	return "Plain Text"
}
