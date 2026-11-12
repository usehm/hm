// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/agent"
	"github.com/usehm/hm/internal/workspace"
)

// Agent tab kinds. A session tab is keyed by its session's ID, a project
// tab by its project's (or "new" before it is saved); the other pages are
// one of a kind.
const (
	agentTabSession   = "session"
	agentTabProjects  = "projects"
	agentTabProject   = "project"
	agentTabArtifacts = "artifacts"
	agentTabSettings  = "settings"
)

// agentTab is one tab of the agent's tab strip.
type agentTab struct {
	kind, key string
	run       *agentRun // for a session
}

func (t *agentTab) id() string {
	if t.run != nil {
		return t.run.session.ID
	}
	return t.key
}

// activeAgentTab is the tab showing; nil before any opens.
func (a *App) activeAgentTab() *agentTab {
	st := &a.agent
	if len(st.tabs) == 0 {
		return nil
	}
	st.active = max(0, min(st.active, len(st.tabs)-1))
	return st.tabs[st.active]
}

// ensureAgentTab opens a new session when the agent shows with no tabs.
func (a *App) ensureAgentTab() {
	if len(a.agent.tabs) == 0 {
		a.openRunTab(a.newRun(nil))
	}
}

// agentTabIndex finds an open tab; -1 when there is none.
func (a *App) agentTabIndex(kind, key string) int {
	for i, t := range a.agent.tabs {
		if t.kind == kind && t.id() == key {
			return i
		}
	}
	return -1
}

// openRunTab shows r in a new tab after the active one.
func (a *App) openRunTab(r *agentRun) {
	st := &a.agent
	t := &agentTab{kind: agentTabSession, run: r}
	a.track(r)
	at := min(st.active+1, len(st.tabs))
	st.tabs = append(st.tabs[:at], append([]*agentTab{t}, st.tabs[at:]...)...)
	a.focusAgentTab(at)
}

// openAgentPage opens a page tab, or focuses it when it is open.
func (a *App) openAgentPage(kind, key string) {
	a.agentOpen = true
	if i := a.agentTabIndex(kind, key); i >= 0 {
		a.focusAgentTab(i)
		return
	}
	st := &a.agent
	at := min(st.active+1, len(st.tabs))
	st.tabs = append(st.tabs[:at], append([]*agentTab{{kind: kind, key: key}}, st.tabs[at:]...)...)
	a.focusAgentTab(at)
}

func (a *App) focusAgentTab(i int) {
	st := &a.agent
	if i < 0 || i >= len(st.tabs) {
		return
	}
	st.active = i
	if r := st.tabs[i].run; r != nil {
		r.focusInput = true
	}
}

// focusRun shows r's tab and the agent.
func (a *App) focusRun(r *agentRun) {
	for i, t := range a.agent.tabs {
		if t.run == r {
			a.agentOpen = true
			a.focusAgentTab(i)
		}
	}
}

// cycleAgentTab moves to the next (1) or previous (-1) tab, wrapping.
func (a *App) cycleAgentTab(d int) {
	n := len(a.agent.tabs)
	if n == 0 {
		return
	}
	a.focusAgentTab(((a.agent.active+d)%n + n) % n)
}

// closeAgentTab closes tab i; a running session asks first.
func (a *App) closeAgentTab(i int) {
	st := &a.agent
	if i < 0 || i >= len(st.tabs) {
		return
	}
	t := st.tabs[i]
	if t.run != nil && t.run.running {
		a.ctx.Dialogs().ShowDanger("Stop the agent?", "This session is still working. Closing its tab stops it.", "Stop and close",
			func() {
				a.stopRun(t.run)
				a.removeAgentTab(t)
			})
		return
	}
	a.removeAgentTab(t)
}

func (a *App) removeAgentTab(t *agentTab) {
	st := &a.agent
	for i, o := range st.tabs {
		if o != t {
			continue
		}
		st.tabs = append(st.tabs[:i], st.tabs[i+1:]...)
		if t.run != nil && t.run.parent == nil {
			t.run.shells.StopAll()
		}
		if t.run != nil {
			a.untrack(t.run)
		}
		if t.kind == agentTabProject {
			delete(st.drafts, t.key)
		}
		if st.active > i || st.active >= len(st.tabs) {
			st.active--
		}
		a.focusAgentTab(max(0, st.active))
		return
	}
}

// closeAgentTabs closes the tabs keep says no to, asking once for running
// sessions.
func (a *App) closeAgentTabs(keep func(*agentTab) bool) {
	var drop []*agentTab
	running := false
	for _, t := range a.agent.tabs {
		if !keep(t) {
			drop = append(drop, t)
			running = running || (t.run != nil && t.run.running)
		}
	}
	do := func() {
		for _, t := range drop {
			if t.run != nil {
				a.stopRun(t.run)
			}
			a.removeAgentTab(t)
		}
	}
	if running {
		a.ctx.Dialogs().ShowDanger("Stop the agents?", "Some of these sessions are still working. Closing their tabs stops them.", "Stop and close", do)
		return
	}
	do()
}

// agentTabStrip is the row of agent tabs with a new-session button.
func (a *App) agentTabStrip(c *ui.Ctx, full bool) ui.View {
	th := c.Theme()
	st := &a.agent
	models := make([]ui.TabModel, len(st.tabs))
	for i, t := range st.tabs {
		models[i] = a.agentTabModel(c, t)
	}
	bg := th.Chrome
	if full {
		bg = th.Surface
	}
	return ui.Row(
		ui.Tabs("agent-tabs", models).Selected(st.active).
			OnSelectItem(func(i int, _ string) { a.focusAgentTab(i) }).
			OnTabClose(a.closeAgentTab).
			OnTabContextMenu(a.agentTabMenu).
			TabBackground(bg).Grow(1),
		ui.IconButton("agent-tab-new", icons.Plus).Tooltip("New session ("+a.key("agent.newTab")+")").
			OnClick(a.newAgentSession),
	).Align(ui.AlignCenter).PaddingRight(th.Spacing.XS).BorderBottom(ui.TokenBorder, th.Stroke.Thin)
}

func (a *App) agentTabModel(c *ui.Ctx, t *agentTab) ui.TabModel {
	th := c.Theme()
	switch t.kind {
	case agentTabProjects:
		return ui.TabModel{Title: "Projects", Icon: icons.FolderKanban}
	case agentTabProject:
		title := "New project"
		if d := a.agent.drafts[t.key]; d != nil && d.Title != "" {
			title = d.Title
		} else if p, ok := a.agent.projects.Get(t.key); ok {
			title = p.Title
		}
		return ui.TabModel{Title: title, Icon: icons.FolderKanban}
	case agentTabArtifacts:
		return ui.TabModel{Title: "Artifacts", Icon: icons.Package}
	case agentTabSettings:
		return ui.TabModel{Title: "Agent Settings", Icon: icons.Settings}
	}
	r := t.run
	m := ui.TabModel{Title: sessionTitle(r.session.Title), Busy: r.running}
	if r.session.Title == "" {
		m.Title = "New session"
	}
	icon, ok := modeIcons[r.mode]
	if !ok {
		icon = icons.Bot
	}
	m.Icon = icon
	switch {
	case r.approval != nil || r.question != nil:
		m.Status = th.Accent // waiting for the user
	case !r.running && r.session.StatusNow() == agent.StatusInterrupted:
		m.Status = th.Warning
	case !r.running && r.errText != "":
		m.Status = th.Error
	}
	return m
}

func (a *App) agentTabMenu(i int) []ui.MenuItem {
	tabs := a.agent.tabs
	if i < 0 || i >= len(tabs) {
		return nil
	}
	t := tabs[i]
	items := []ui.MenuItem{
		{Label: "Close", Shortcut: a.key("agent.closeTab"), OnSelect: func() { a.closeAgentTab(i) }},
		{Label: "Close Others", Disabled: len(tabs) < 2, OnSelect: func() { a.closeAgentTabs(func(o *agentTab) bool { return o == t }) }},
		{Label: "Close All", OnSelect: func() { a.closeAgentTabs(func(*agentTab) bool { return false }) }},
	}
	if r := t.run; r != nil && len(r.session.Snapshot()) > 0 {
		items = append(items, ui.MenuSeparator,
			ui.MenuItem{Label: "Rename…", Icon: icons.Pencil, OnSelect: func() { a.renameSession(r.session.ID) }})
		items = append(items, a.sessionActions(r.session.ID)...)
	}
	return items
}

// agentPage is a tab that is not a session.
func (a *App) agentPage(c *ui.Ctx, t *agentTab) ui.View {
	switch t.kind {
	case agentTabProjects:
		return a.projectsPage(c)
	case agentTabProject:
		return a.projectPage(c, t)
	case agentTabSettings:
		return a.settingsPageOf(c, agentSettingsCategories, &a.agent.settingsCat)
	}
	return ui.ViewOf(ui.EmptyState("Artifacts", "Files the agent produces will be kept here, in the storage picked in "+
		"Agent Settings → Artifacts: a GCS or S3 bucket, or a git repository.").EmptyIcon(icons.Package)).Grow(1)
}

// agentSettingsCategories are the Settings categories the agent's settings
// tab shows.
var agentSettingsCategories = []string{"agent", "security", "artifacts", "completion"}

// sessionList is the saved sessions with at least one message, newest
// first, read again when stale.
func (a *App) sessionList() []agent.Summary {
	st := &a.agent
	if st.sessionsStale || st.sessions == nil {
		st.sessionsStale = false
		st.sessions = st.sessions[:0]
		if st.store != nil {
			for _, s := range st.store.List() {
				if s.Turns > 0 {
					st.sessions = append(st.sessions, s)
				}
			}
		}
	}
	return st.sessions
}

// agentSidebar is the explorer's place while the agent is full screen: a
// new session, projects, artifacts, the session history with a filter, and
// settings.
func (a *App) agentSidebar(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	nav := func(id, label string, icon icons.Icon, kind string) *ui.Node {
		b := ui.Button(id, ui.Text(label)).Ghost().HoverFill().IconStart(icon).Justify(ui.JustifyStart).
			OnClick(func() { a.openAgentPage(kind, "") })
		if t := a.activeAgentTab(); t != nil && t.kind == kind {
			b = b.Background(ui.TokenListActive)
		}
		return b
	}
	rows := a.sidebarSessions(c)
	if len(rows) == 0 {
		msg := "No sessions yet"
		if st.sideQuery != "" {
			msg = "No sessions match"
		}
		// Lined up with the session titles, past a row's padding and mark.
		rows = append(rows, ui.Muted(msg).PaddingLeft(th.Spacing.XS+8+th.Spacing.S).PaddingTop(th.Spacing.XS))
	}
	filter := ui.IconButton("agent-side-filter-toggle", icons.ListFilter).Tooltip("Filter sessions").OnClick(func() {
		st.sideFilter = !st.sideFilter
		st.sideFocus = st.sideFilter
		if !st.sideFilter {
			st.sideQuery = ""
		}
	})
	if st.sideFilter {
		filter = filter.Background(ui.TokenListActive)
	}
	col := []ui.View{
		ui.Column(
			ui.Button("agent-side-new", ui.Text("New chat")).Ghost().HoverFill().IconStart(icons.SquarePen).Justify(ui.JustifyStart).
				Tooltip("New session ("+a.key("agent.newTab")+")").OnClick(a.newAgentSession),
			nav("agent-side-projects", "Projects", icons.FolderKanban, agentTabProjects),
			nav("agent-side-artifacts", "Artifacts", icons.Package, agentTabArtifacts),
		).Gap(th.Spacing.XXS).PaddingXY(th.Spacing.S, th.Spacing.S),
		ui.Row(
			ui.Caption("SESSIONS").Grow(1),
			filter,
		).Align(ui.AlignCenter).Height(30).PaddingLeft(th.Spacing.M).PaddingRight(th.Spacing.XS),
	}
	if st.sideFilter {
		col = append(col, ui.TextField("agent-side-filter", st.sideQuery).
			Placeholder("Search sessions").
			IconStart(icons.Search).
			FocusNow(st.sideFocus).
			OnChange(func(q string) { st.sideQuery = q }).
			MarginXY(th.Spacing.S, th.Spacing.XS))
		st.sideFocus = false
	}
	col = append(col,
		ui.Scroll("agent-side-sessions", ui.Column(rows...).Gap(th.Spacing.XXS).PaddingXY(th.Spacing.S, th.Spacing.XS)).
			Background(ui.TokenChrome).Grow(1),
		ui.Column(nav("agent-side-settings", "Agent Settings", icons.Settings, agentTabSettings)).
			PaddingXY(th.Spacing.S, th.Spacing.S).BorderTop(ui.TokenBorder, th.Stroke.Thin),
	)
	return ui.Column(col...).Grow(1).Background(ui.TokenChrome)
}

// sidebarSessions are the session rows matching the filter, each followed
// by the sub-agent sessions it started and the sessions forked from it.
func (a *App) sidebarSessions(c *ui.Ctx) []ui.View {
	q := strings.ToLower(strings.TrimSpace(a.agent.sideQuery))
	list := a.sessionList()
	listed := map[string]bool{}
	for _, s := range list {
		listed[s.ID] = true
	}
	// A session nests under the one it came from while that one is listed.
	source := func(s agent.Summary) string {
		for _, id := range []string{s.ParentID, s.ForkOf} {
			if id != "" && listed[id] {
				return id
			}
		}
		return ""
	}
	children := map[string][]agent.Summary{}
	for _, s := range list {
		if src := source(s); src != "" {
			children[src] = append(children[src], s)
		}
	}
	var rows []ui.View
	var add func(s agent.Summary, depth int)
	add = func(s agent.Summary, depth int) {
		rows = append(rows, a.sessionRow(c, s, depth))
		for _, ch := range children[s.ID] {
			add(ch, depth+1)
		}
	}
	for _, s := range list {
		if source(s) != "" {
			continue
		}
		if q != "" && !a.sessionMatches(s, q) {
			continue
		}
		add(s, 0)
	}
	return rows
}

func (a *App) sessionMatches(s agent.Summary, q string) bool {
	hay := strings.ToLower(s.Title + " " + filepath.Base(s.Root) + " " + s.Mode)
	if p, ok := a.agent.projects.Get(s.ProjectID); ok {
		hay += " " + strings.ToLower(p.Title)
	}
	return strings.Contains(hay, q)
}

// sessionRow is one session in the sidebar: a status mark and the title,
// with a menu, shown on hover, to rename or remove it.
func (a *App) sessionRow(c *ui.Ctx, s agent.Summary, depth int) ui.View {
	th := c.Theme()
	id := s.ID
	idx := a.agentTabIndex(agentTabSession, id)
	var run *agentRun
	if idx >= 0 {
		run = a.agent.tabs[idx].run
	}
	var mark ui.View
	switch {
	case run != nil && run.running:
		mark = ui.Spinner("agent-side-spin-"+id, 12)
	case s.Status == agent.StatusInterrupted || s.Status == agent.StatusRunning && run == nil:
		mark = statusDot(th.Warning).Tooltip("Interrupted")
	case idx >= 0:
		mark = statusDot(th.Accent).Tooltip("Open in a tab")
	default:
		mark = statusDot(render.Color{}).Tooltip("")
	}
	title := sessionTitle(s.Title)
	switch {
	case depth > 0 && s.ForkOf != "" && s.ParentID == "":
		title = "↱ " + title
	case depth > 0:
		title = "↳ " + title
	}
	body := ui.Row(
		mark,
		ui.Text(title).Ellipsis(ui.EllipsisEnd).Grow(1).Shrink(1),
	).Gap(th.Spacing.S).Align(ui.AlignCenter).Grow(1).Shrink(1)
	active := false
	if t := a.activeAgentTab(); t != nil && t.kind == agentTabSession && t.id() == id {
		active = true
	}
	// The row and its menu button share one height, with room above and
	// below the title so the rows are easy to tell apart.
	h := th.Typography.Body.LineHeight + 2*th.Spacing.XS
	row := ui.HoverGroup("agent-side-row-"+id, func(hovered bool) ui.View {
		btn := ui.Button("agent-side-s-"+id, body).Ghost().HoverFill().Height(h).Grow(1).Shrink(1).
			Tooltip(sessionTitle(s.Title)).
			OnClick(func() { a.resumeAgentSession(id) })
		switch {
		case active:
			btn = btn.Background(ui.TokenListActive)
		case hovered:
			// Lit while the pointer is on the menu button too.
			btn = btn.Background(ui.TokenListHover)
		}
		return ui.Row(btn,
			ui.IconButton("agent-side-more-"+id, icons.Ellipsis).Height(h).Tooltip("More").Reveal(hovered).Menu(slices.Concat(
				[]ui.MenuItem{{Label: "Rename…", Icon: icons.Pencil, OnSelect: func() { a.renameSession(id) }}},
				a.sessionActions(id),
				[]ui.MenuItem{ui.MenuSeparator, {Label: "Remove", Icon: icons.Trash2, Danger: true, OnSelect: func() { a.removeSession(id) }}},
			)),
		).Gap(th.Spacing.XXS).Align(ui.AlignCenter)
	})
	if depth > 0 {
		row = row.PaddingLeft(th.Spacing.M * float32(min(depth, 4)))
	}
	return row
}

func statusDot(col render.Color) *ui.Node {
	return ui.Row().Width(8).Height(8).Radius(4).Style(ui.BackgroundColor(col)).Shrink(0)
}

// renameSession asks for a session's new title.
func (a *App) renameSession(id string) {
	title := ""
	for _, s := range a.sessionList() {
		if s.ID == id {
			title = s.Title
		}
	}
	a.ctx.Dialogs().ShowInputValue("Rename session", "Title", title, func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if i := a.agentTabIndex(agentTabSession, id); i >= 0 {
			s := a.agent.tabs[i].run.session
			s.SetTitle(v)
			if err := a.agent.store.Save(s); err != nil {
				a.fail("Could not rename the session", err)
			}
		} else if s, err := a.agent.store.Load(id); err == nil {
			s.SetTitle(v)
			if err := a.agent.store.Save(s); err != nil {
				a.fail("Could not rename the session", err)
			}
		}
		a.agent.sessionsStale = true
	}, nil)
}

// removeSession deletes a saved session after asking, closing its tab.
func (a *App) removeSession(id string) {
	a.ctx.Dialogs().ShowDanger("Remove this session?", "Its conversation is deleted. Files the agent changed stay as they are.", "Remove",
		func() {
			if i := a.agentTabIndex(agentTabSession, id); i >= 0 {
				t := a.agent.tabs[i]
				a.stopRun(t.run)
				a.removeAgentTab(t)
			}
			if err := a.agent.store.Delete(id); err != nil {
				a.fail("Could not remove the session", err)
			}
			a.agent.sessionsStale = true
		})
}

// projectsPage lists the projects as cards.
func (a *App) projectsPage(c *ui.Ctx) ui.View {
	th := c.Theme()
	counts := map[string]int{}
	for _, s := range a.sessionList() {
		counts[s.ProjectID]++
	}
	head := ui.Row(
		ui.Column(
			ui.Title("Projects"),
			ui.Row(ui.Caption("A project's context is given to the agent before every task in its sessions.").Ellipsis(ui.EllipsisEnd).Shrink(1)),
		).Gap(th.Spacing.XXS).Grow(1).Shrink(1),
		ui.Button("projects-new", ui.Text("New project")).Primary().IconStart(icons.Plus).
			OnClick(func() { a.openAgentPage(agentTabProject, "new") }),
	).Gap(th.Spacing.M).Align(ui.AlignCenter)
	list := a.agent.projects.List()
	if len(list) == 0 {
		return ui.Column(head, ui.ViewOf(ui.EmptyState("No projects yet",
			"Make one to keep notes the agent should know: goals, conventions, links.").EmptyIcon(icons.FolderKanban)).Grow(1)).
			Gap(th.Spacing.L).PaddingXY(th.Spacing.XL, th.Spacing.XL).Grow(1)
	}
	cards := make([]ui.View, 0, len(list))
	for _, p := range list {
		id := p.ID
		n := counts[id]
		desc := p.Description
		if desc == "" {
			desc = "No description"
		}
		cards = append(cards, ui.ClickableCard("project-card-"+id, p.Title, "",
			ui.Paragraph(firstLineOf(desc)).Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted))).
			Actions(ui.IconButton("project-more-"+id, icons.Ellipsis).Menu([]ui.MenuItem{
				{Label: "Edit", Icon: icons.Pencil, OnSelect: func() { a.openAgentPage(agentTabProject, id) }},
				{Label: "New session", Icon: icons.SquarePen, OnSelect: func() { a.newSessionInProject(id) }},
				ui.MenuSeparator,
				{Label: "Remove", Icon: icons.Trash2, Danger: true, OnSelect: func() { a.removeProject(id) }},
			})).
			Footer(ui.Caption(fmt.Sprintf("%d %s · updated %s", n, plural(n, "session"), ago(p.Updated)))).
			Flat().Width(280).
			OnClick(func() { a.openAgentPage(agentTabProject, id) }))
	}
	return ui.Scroll("projects-scroll", ui.Column(head, ui.Row(cards...).Wrap().Gap(th.Spacing.M)).
		Gap(th.Spacing.L).PaddingXY(th.Spacing.XL, th.Spacing.XL)).Grow(1)
}

// projectPage edits one project. Changes are kept as a draft until saved.
func (a *App) projectPage(c *ui.Ctx, t *agentTab) ui.View {
	th := c.Theme()
	st := &a.agent
	if st.drafts == nil {
		st.drafts = map[string]*agent.Project{}
	}
	d := st.drafts[t.key]
	saved, exists := st.projects.Get(t.key)
	if d == nil {
		d = &agent.Project{}
		if exists {
			*d = saved
		}
		st.drafts[t.key] = d
	}
	dirty := !exists || d.Title != saved.Title || d.Description != saved.Description || d.Context != saved.Context
	save := func() {
		if strings.TrimSpace(d.Title) == "" {
			a.toast("Give the project a title", ui.ToastWarning)
			return
		}
		p, err := st.projects.Put(*d)
		if err != nil {
			a.fail("Could not save the project", err)
			return
		}
		delete(st.drafts, t.key)
		t.key = p.ID
		a.toast("Saved "+p.Title, ui.ToastSuccess)
	}
	id := "project-" + t.key
	actions := []ui.View{
		ui.Button(id+"-save", ui.Text("Save")).Primary().Disabled(!dirty).OnClick(save),
	}
	if exists {
		pid := t.key
		actions = append(actions,
			ui.Button(id+"-session", ui.Text("New session")).IconStart(icons.SquarePen).OnClick(func() { a.newSessionInProject(pid) }),
			ui.Spacer(),
			ui.Button(id+"-remove", ui.Text("Remove")).Subtle().IconStart(icons.Trash2).OnClick(func() { a.removeProject(pid) }))
	}
	title := d.Title
	if title == "" {
		title = "New project"
	}
	return ui.Scroll(id+"-scroll", ui.Column(
		ui.Title(title),
		ui.Form(id+"-form",
			ui.FormText(id+"-title", "Title", "Shown on the card and in the composer", d.Title,
				func(v string) { d.Title = v }),
		).Grouped(),
		ui.Column(
			ui.Strong("Description"),
			ui.Caption("What the project is, in a line or two"),
			ui.TextArea(id+"-desc", d.Description).Rows(2, 6).OnChange(func(v string) { d.Description = v }),
		).Gap(th.Spacing.XS),
		ui.Column(
			ui.Strong("Context"),
			ui.Caption("Told to the agent before it starts a task in one of this project's sessions: goals, "+
				"conventions, where things live, what to avoid."),
			ui.TextArea(id+"-context", d.Context).Rows(8, 30).OnChange(func(v string) { d.Context = v }),
		).Gap(th.Spacing.XS),
		ui.Row(actions...).Gap(th.Spacing.S),
	).Gap(th.Spacing.L).PaddingXY(th.Spacing.XL, th.Spacing.XL).MaxWidth(800)).Grow(1)
}

// newSessionInProject opens a new session that starts with the project's
// context.
func (a *App) newSessionInProject(id string) {
	r := a.newRun(nil)
	r.session.ProjectID = id
	a.openRunTab(r)
	a.agentOpen = true
}

// removeProject deletes a project after asking; its sessions stay.
func (a *App) removeProject(id string) {
	p, ok := a.agent.projects.Get(id)
	if !ok {
		return
	}
	a.ctx.Dialogs().ShowDanger("Remove "+p.Title+"?", "Its sessions stay, without the project's context.", "Remove",
		func() {
			if err := a.agent.projects.Delete(id); err != nil {
				a.fail("Could not remove the project", err)
				return
			}
			if i := a.agentTabIndex(agentTabProject, id); i >= 0 {
				a.removeAgentTab(a.agent.tabs[i])
			}
		})
}

// agentTabsState is the open agent tabs for the workspace session.
func (a *App) agentTabsState() ([]workspace.AgentTab, int) {
	var out []workspace.AgentTab
	active := 0
	for i, t := range a.agent.tabs {
		switch {
		case t.run != nil && len(t.run.session.Snapshot()) == 0:
			continue // nothing saved to reopen
		case t.kind == agentTabProject && t.key == "new":
			continue
		}
		if i == a.agent.active {
			active = len(out)
		}
		out = append(out, workspace.AgentTab{Kind: t.kind, Key: t.id()})
	}
	return out, active
}

// restoreAgentTabs reopens the tabs a workspace session lists.
func (a *App) restoreAgentTabs(tabs []workspace.AgentTab, active int) {
	for _, t := range tabs {
		switch t.Kind {
		case agentTabSession:
			s, err := a.agent.store.Load(t.Key)
			if err != nil {
				continue
			}
			if s.Status == agent.StatusRunning {
				s.Status = agent.StatusInterrupted
				s.Repair()
				_ = a.agent.store.Save(s)
			}
			r := a.newRun(s)
			a.track(r)
			a.agent.tabs = append(a.agent.tabs, &agentTab{kind: agentTabSession, run: r})
		case agentTabProject:
			if _, ok := a.agent.projects.Get(t.Key); ok {
				a.agent.tabs = append(a.agent.tabs, &agentTab{kind: t.Kind, key: t.Key})
			}
		case agentTabProjects, agentTabArtifacts, agentTabSettings:
			a.agent.tabs = append(a.agent.tabs, &agentTab{kind: t.Kind, key: t.Key})
		}
	}
	a.focusAgentTab(min(active, len(a.agent.tabs)-1))
}
