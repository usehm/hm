package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"
)

// A session is a tree: going back to an earlier message and going on from
// there starts a branch, and the old one stays. /tree shows every branch,
// /fork copies the conversation up to a point into a new session, and
// /label bookmarks a message.

// Filters of the /tree view.
const (
	treeAll = iota
	treeUser
	treeAssistant
	treeTools
	treeLabels
)

var treeFilters = []ui.SegmentItem{
	{Label: "All", Value: "all"},
	{Label: "User", Value: "user"},
	{Label: "Assistant", Value: "assistant"},
	{Label: "Tools", Value: "tools"},
	{Label: "Labels", Value: "labels"},
}

// treeRow is one line of the /tree view.
type treeRow struct {
	entry  agent.Entry
	depth  int
	onPath bool
	head   bool
	label  string
	text   string
	prompt bool // a message the user typed
	// note is the summary of the branch that ends above, on a row of its
	// own.
	note string
}

// treeRows lays out the session's tree for /tree: a run of entries stays
// at one depth, and each branch after a point with several is indented
// one more. Only messages and compactions get rows; filter and query
// (any case) choose among them.
func treeRows(entries []agent.Entry, head string, labels map[string]string, notes map[string]agent.BranchSummary, filter int, query string) []treeRow {
	kids := map[string][]agent.Entry{}
	parent := map[string]string{}
	for _, e := range entries {
		kids[e.ParentID] = append(kids[e.ParentID], e)
		parent[e.ID] = e.ParentID
	}
	onPath := map[string]bool{}
	for id, n := head, 0; id != "" && n <= len(entries); id, n = parent[id], n+1 {
		onPath[id] = true
	}
	query = strings.ToLower(strings.TrimSpace(query))
	var rows []treeRow
	var walk func(e agent.Entry, depth int)
	walk = func(e agent.Entry, depth int) {
		for {
			if r, ok := treeRowOf(e, filter); ok {
				r.depth, r.onPath, r.head, r.label = depth, onPath[e.ID], e.ID == head, labels[e.ID]
				if r.label != "" && filter == treeLabels || filter != treeLabels {
					if query == "" || strings.Contains(strings.ToLower(r.text+" "+r.label), query) {
						rows = append(rows, r)
					}
				}
			}
			next := kids[e.ID]
			if len(next) == 0 {
				if n, ok := notes[e.ID]; ok && filter != treeLabels &&
					(query == "" || strings.Contains(strings.ToLower(n.Text), query)) {
					rows = append(rows, treeRow{entry: e, depth: depth, note: n.Text})
				}
				return
			}
			if len(next) > 1 {
				for _, k := range next {
					walk(k, depth+1)
				}
				return
			}
			e = next[0]
		}
	}
	roots := kids[""]
	for _, e := range roots {
		d := 0
		if len(roots) > 1 {
			d = 1
		}
		walk(e, d)
	}
	return rows
}

// treeRowOf is the row for e under filter; ok is false when it has none.
func treeRowOf(e agent.Entry, filter int) (treeRow, bool) {
	r := treeRow{entry: e}
	switch {
	case e.Kind == agent.KindCompaction && e.Compaction != nil:
		r.text = fmt.Sprintf("Compacted %s → %s tokens", agent.Tokens(e.Compaction.TokensBefore), agent.Tokens(e.Compaction.TokensAfter))
		return r, filter == treeAll || filter == treeLabels
	case e.Kind != agent.KindMessage || e.Message == nil:
		return r, false
	}
	m := *e.Message
	if t := agent.PromptText(m); t != "" {
		r.text, r.prompt = oneLine(typedText(t), 120), true
		return r, filter != treeAssistant && filter != treeTools
	}
	var names []string
	for _, b := range m.Blocks {
		if b.Type == provider.ToolUse || b.Type == provider.ToolResult {
			names = append(names, b.ToolName)
		}
	}
	if m.Role == provider.User {
		r.text = "Results of " + strings.Join(names, ", ")
		return r, filter == treeTools || filter == treeLabels
	}
	text := strings.TrimSpace(m.TextOf())
	switch {
	case text != "" && len(names) > 0:
		r.text = oneLine(text, 100) + " · called " + strings.Join(names, ", ")
	case text != "":
		r.text = oneLine(text, 120)
	default:
		r.text = "Called " + strings.Join(names, ", ")
	}
	tools := len(names) > 0
	return r, filter == treeAll || filter == treeLabels || filter == treeAssistant && text != "" || filter == treeTools && tools
}

// oneLine is s's first line, cut to about n characters.
func oneLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// openTree shows r's session tree.
func (a *App) openTree(r *agentRun) {
	r.treeQuery, r.treeFilter = "", treeAll
	w, h := a.ctx.Viewport()
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title:   "Session tree",
		Width:   max(560, w*0.6),
		Height:  max(420, h*0.75),
		Body:    func(c *ui.Ctx) ui.View { return a.treeBody(c, r) },
		Actions: []ui.DialogAction{{Label: "Close", OnClick: func() { a.ctx.Dialogs().Close() }}},
	})
}

func (a *App) treeBody(c *ui.Ctx, r *agentRun) ui.View {
	th := c.Theme()
	s := r.session
	rows := treeRows(s.Entries(), s.Head, s.Labels(), s.BranchSummaries(), r.treeFilter, r.treeQuery)
	top := ui.Row(
		ui.TextField(r.wid("tree-query"), r.treeQuery).Placeholder("Search messages and labels").
			IconStart(icons.Search).DefaultFocus().OnChange(func(q string) { r.treeQuery = q }).Grow(1),
		ui.Segmented(r.wid("tree-filter"), treeFilters...).Selected(r.treeFilter).
			OnSelectItem(func(i int, _ string) { r.treeFilter = i }).Height(26),
	).Gap(th.Spacing.S).Align(ui.AlignCenter)
	hint := ui.Caption("Choose a message to edit and send it again, or any other entry to go on from there. " +
		"Files on disk stay as they are.")
	if r.running {
		hint = ui.Caption("Wait for the turn to finish to change branches.")
	}
	var list []ui.View
	for i, row := range rows {
		list = append(list, a.treeRowView(c, r, i, row))
	}
	if len(list) == 0 {
		list = append(list, ui.Caption("Nothing matches."))
	}
	return ui.Column(top, hint,
		ui.Scroll(r.wid("tree-scroll"), ui.Column(list...).Gap(th.Spacing.XXS)).Grow(1),
	).Gap(th.Spacing.S).Grow(1)
}

func (a *App) treeRowView(c *ui.Ctx, r *agentRun, i int, row treeRow) ui.View {
	th := c.Theme()
	indent := float32(row.depth) * th.Spacing.L
	if row.note != "" {
		return ui.Row(ui.Icon(icons.GitBranch, 12, th.ForegroundMuted), ui.Caption(row.note).Grow(1)).
			Gap(th.Spacing.XS).PaddingLeft(indent+th.Spacing.L).PaddingXY(th.Spacing.S, th.Spacing.XXS)
	}
	icon, color := icons.Bot, th.ForegroundMuted
	switch {
	case row.entry.Kind == agent.KindCompaction:
		icon = icons.Shrink
	case row.prompt:
		icon = icons.User
	case row.entry.Message != nil && row.entry.Message.Role == provider.User:
		icon = icons.Wrench
	}
	mark := ui.Icon(icons.Dot, 12, th.ForegroundMuted)
	switch {
	case row.head:
		mark = ui.Icon(icons.CircleDot, 12, th.Accent).Tooltip("You are here")
	case row.onPath:
		mark = ui.Icon(icons.Dot, 12, th.Accent)
		color = th.Foreground
	}
	items := []ui.View{mark, ui.Icon(icon, 12, color), ui.Text(row.text).Ellipsis(ui.EllipsisEnd).Grow(1).Shrink(1)}
	if row.label != "" {
		items = append(items, ui.Badge(row.label))
	}
	items = append(items, ui.Caption(row.entry.ID))
	e := row.entry
	id := r.wid(fmt.Sprintf("tree-%d-%s", i, e.ID))
	return ui.HoverGroup(id+"-hover", func(hovered bool) ui.View {
		btn := ui.Button(id, ui.Row(items...).Gap(th.Spacing.XS).Align(ui.AlignCenter)).Ghost().HoverFill().
			Disabled(r.running).Grow(1).Shrink(1).OnClick(func() {
			a.ctx.Dialogs().Close()
			if row.prompt {
				a.editAndResend(r, e)
			} else {
				a.branchTo(r, e.ID)
			}
		})
		if row.onPath {
			btn = btn.Background(ui.TokenListActive)
		}
		return ui.Row(btn,
			ui.IconButton(id+"-fork", icons.GitFork).Tooltip("Fork a new session from here").Reveal(hovered).
				Disabled(r.running).OnClick(func() {
				a.ctx.Dialogs().Close()
				a.forkAt(r, e.ID)
			}),
		).Gap(th.Spacing.XXS).Align(ui.AlignCenter).PaddingLeft(indent)
	})
}

// hasPrompt reports whether r's active path has a message the user typed.
func (r *agentRun) hasPrompt() bool {
	_, _, ok := r.session.LastPrompt()
	return ok
}

// branchTo moves r's session to entry id, so the next message goes on
// from there, and asks for a summary of the branch left when that is on.
func (a *App) branchTo(r *agentRun, id string) {
	if r.running {
		r.errText = "Wait for the turn to finish, then go back"
		return
	}
	s := r.session
	old := s.Head
	if err := s.Branch(id); err != nil {
		r.errText = err.Error()
		return
	}
	if s.Mode != "" {
		r.mode = s.Mode
	}
	if s.Provider != "" {
		r.provider = s.Provider
	}
	r.todos.Set(s.Todos)
	// The files the last turn changed belong to the branch left behind.
	r.lastCheckpoint = nil
	r.replay, r.errText, r.info = false, "", ""
	a.saveRun(r)
	a.summarizeBranch(r, old)
}

// editAndResend goes back to before the user's message e and puts its
// text in the message box, to change and send as a new branch.
func (a *App) editAndResend(r *agentRun, e agent.Entry) {
	if e.Message == nil || r.running {
		return
	}
	text := typedText(agent.PromptText(*e.Message))
	a.branchTo(r, e.ParentID)
	if r.session.Head == e.ParentID {
		r.input, r.focusInput = text, true
	}
}

// turnEnd is the last entry of the turn that the user's message id
// starts: what "Branch here" goes back to. ok is false for the latest
// turn, which has nothing after it to leave.
func turnEnd(path []agent.Entry, id string) (string, bool) {
	start := -1
	for i, e := range path {
		if e.ID == id {
			start = i
		}
		if start >= 0 && i > start && e.Message != nil && agent.PromptText(*e.Message) != "" {
			return path[i-1].ID, true
		}
	}
	return "", false
}

// forkAt opens a new session holding r's conversation up to entry id
// (Head when empty). A message the user typed is left out and put in the
// new session's message box instead.
func (a *App) forkAt(r *agentRun, id string) {
	s := r.session
	if id == "" {
		id = s.Head
	}
	if id == "" {
		r.errText = "Nothing to fork yet"
		return
	}
	e, ok := s.Entry(id)
	if !ok {
		r.errText = "No entry " + id + " in this session; /tree shows the IDs"
		return
	}
	at, text := id, ""
	if e.Message != nil {
		if t := agent.PromptText(*e.Message); t != "" {
			at, text = e.ParentID, typedText(t)
		}
	}
	f, err := s.Fork(at)
	if err != nil {
		r.errText = err.Error()
		return
	}
	if err := a.agent.store.Save(f); err != nil {
		a.fail("Could not save the forked session", err)
		return
	}
	a.agent.sessionsStale = true
	nr := a.newRun(f)
	nr.input = text
	a.openRunTab(nr)
	a.toast("Forked into a new session", ui.ToastSuccess)
}

// findEntry is the entry of r's session whose ID is or starts with id.
func findEntry(s *agent.Session, id string) (string, error) {
	if _, ok := s.Entry(id); ok {
		return id, nil
	}
	found := ""
	for _, e := range s.Entries() {
		if strings.HasPrefix(e.ID, id) {
			if found != "" {
				return "", fmt.Errorf("%s matches more than one entry", id)
			}
			found = e.ID
		}
	}
	if found == "" {
		return "", fmt.Errorf("No entry %s in this session; /tree shows the IDs", id)
	}
	return found, nil
}

// labelLast bookmarks the active path's latest message; empty text
// removes its label.
func (a *App) labelLast(r *agentRun, text string) {
	ids := r.session.MessageIDs()
	if len(ids) == 0 {
		r.errText = "Nothing to label yet"
		return
	}
	if err := r.session.Label(ids[len(ids)-1], text); err != nil {
		r.errText = err.Error()
		return
	}
	a.saveRun(r)
	if text == "" {
		r.info = "Label removed"
	} else {
		r.info = "Labelled \"" + text + "\"; /tree finds it"
	}
}

// summarizeBranch asks for a few lines on the branch ending at leaf that
// r's session left, when agent.branch_summary is on.
func (a *App) summarizeBranch(r *agentRun, leaf string) {
	cfg := a.cfg.Current().Agent.BranchSummary
	s := r.session
	if !cfg.Enabled || leaf == "" || len(s.Abandoned(leaf)) < cfg.MinMessages {
		return
	}
	h := a.agentHost()
	prov, _, err := h.Provider(r.provider)
	if err != nil {
		return
	}
	cc, err := h.Compaction(r.provider, prov)
	if err != nil {
		return
	}
	ag := &agent.Agent{Compaction: cc}
	least := cfg.MinMessages
	go func() {
		err := ag.SummarizeBranch(context.Background(), s, leaf, least)
		a.onUI(func() {
			if err != nil {
				a.notifs.Add("Could not summarise the branch left: "+err.Error(), ui.ToastWarning)
				return
			}
			_ = a.agent.store.Save(s)
		})
	}()
}

// saveRun writes r's session, warning when it cannot.
func (a *App) saveRun(r *agentRun) {
	if err := a.agent.store.Save(r.session); err != nil {
		a.notifs.Add("Could not save the agent session: "+err.Error(), ui.ToastWarning)
	}
	a.agent.sessionsStale = true
}
