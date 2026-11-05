package app

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/diff"
)

func sortStrings(s []string) { sort.Strings(s) }

// maxResultLines is how much of a tool result a card shows opened.
const maxResultLines = 40

// agentView is the agent docked beside the editor: the tab strip, then the
// active tab.
func (a *App) agentView(c *ui.Ctx) ui.View {
	th := c.Theme()
	a.ensureAgentTab()
	t := a.activeAgentTab()
	var body ui.View
	if t.run != nil {
		body = a.sessionPanel(c, t.run)
	} else {
		body = a.agentPage(c, t)
	}
	return ui.Column(a.agentTabStrip(c, false), ui.ViewOf(body).Grow(1)).
		Grow(1).Background(ui.TokenChrome).BorderLeft(ui.TokenBorder, th.Stroke.Thin)
}

// sessionPanel is a session in the docked agent.
func (a *App) sessionPanel(c *ui.Ctx, r *agentRun) ui.View {
	th := c.Theme()
	body := []ui.View{a.agentHeader(c, r, false)}
	if a.agentFresh(r) {
		// Before the first message the composer sits at the top, tall.
		body = append(body, ui.Column(a.composer(c, r, true), ui.Caption(
			"Enter to send · Shift+Enter for a new line")).
			Gap(th.Spacing.S).Padding(th.Spacing.M))
		if r.errText != "" {
			body = append(body, a.agentError(c, r))
		}
		if r.info != "" {
			body = append(body, a.agentInfo(c, r))
		}
		body = append(body, ui.Spacer())
		return ui.Column(body...).Grow(1)
	}
	body = append(body, a.transcript(c, r))
	body = append(body, a.sessionFooter(c, r)...)
	if !r.replay {
		body = append(body, ui.Column(a.composer(c, r, false)).Padding(th.Spacing.S))
	}
	return ui.Column(body...).Grow(1)
}

// sessionFooter is what sits between the transcript and the composer: the
// replay controls, the interrupted notice, tasks, cards waiting for the
// user, errors and queued messages.
func (a *App) sessionFooter(c *ui.Ctx, r *agentRun) []ui.View {
	var out []ui.View
	if r.replay {
		return append(out, a.replayBar(c, r))
	}
	if v := a.interruptedNotice(c, r); v != nil {
		out = append(out, v)
	}
	if t := a.todoView(c, r); t != nil {
		out = append(out, t)
	}
	if p := r.approval; p != nil {
		out = append(out, a.approvalCard(c, r, p))
	}
	if q := r.question; q != nil {
		out = append(out, a.questionCard(c, r, q))
	}
	if r.errText != "" {
		out = append(out, a.agentError(c, r))
	}
	if r.info != "" {
		out = append(out, a.agentInfo(c, r))
	}
	if v := a.queueView(c, r); v != nil {
		out = append(out, v)
	}
	return out
}

// agentFresh reports whether the session has nothing in it yet.
func (a *App) agentFresh(r *agentRun) bool {
	return !r.running && len(r.session.Snapshot()) == 0
}

func (a *App) agentError(c *ui.Ctx, r *agentRun) ui.View {
	th := c.Theme()
	return ui.Row(
		ui.Paragraph(r.errText).Style(ui.Spec{}.TextColor(ui.TokenError)).Grow(1),
		ui.IconButton(r.wid("err-close"), icons.X).OnClick(func() { r.errText = "" }),
	).Gap(th.Spacing.XS).PaddingXY(th.Spacing.M, th.Spacing.XXS)
}

// agentInfo is a notice for the user, such as /context's answer.
func (a *App) agentInfo(c *ui.Ctx, r *agentRun) ui.View {
	th := c.Theme()
	return ui.Row(
		ui.Paragraph(r.info).Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted)).Grow(1),
		ui.IconButton(r.wid("info-close"), icons.X).OnClick(func() { r.info = "" }),
	).Gap(th.Spacing.XS).PaddingXY(th.Spacing.M, th.Spacing.XXS)
}

// contextButton lists the session's instruction files; nil when there are
// none.
func (a *App) contextButton(r *agentRun) ui.View {
	ctx := a.agentContext(r)
	files := ctx.Loaded()
	if len(files) == 0 && len(ctx.Warnings) == 0 {
		return nil
	}
	var items []ui.MenuItem
	for _, f := range files {
		path := f.Path
		items = append(items, ui.MenuItem{Label: f.Rel, Icon: icons.FileText, OnSelect: func() { a.openFile(path) }})
	}
	for _, w := range ctx.Warnings {
		items = append(items, ui.MenuItem{Label: w, Icon: icons.TriangleAlert, Disabled: true})
	}
	tip := fmt.Sprintf("%d instruction files in the system prompt", len(files))
	if len(files) == 1 {
		tip = "1 instruction file in the system prompt"
	}
	return ui.IconButton(r.wid("context"), icons.BookText).Tooltip(tip).Menu(items)
}

// wid is a widget id unique to r, so tabs keep their own scroll, focus and
// hover.
func (r *agentRun) wid(name string) string { return "agent-" + r.session.ID + "-" + name }

// interruptedNotice offers to continue a session whose turn was cut off
// when hm stopped.
func (a *App) interruptedNotice(c *ui.Ctx, r *agentRun) ui.View {
	if r.running || r.session.StatusNow() != agent.StatusInterrupted {
		return nil
	}
	th := c.Theme()
	dismiss := func() {
		r.session.SetStatus(agent.StatusIdle)
		_ = a.agent.store.Save(r.session)
	}
	return ui.Row(
		ui.Icon(icons.TriangleAlert, th.Metrics.IconSizeSM, th.Warning),
		ui.Paragraph("This task was interrupted when hm stopped. Background shells and "+
			"\"allow for session\" answers did not survive.").Grow(1),
		ui.Button(r.wid("resume"), ui.Text("Resume")).Primary().OnClick(func() {
			dismiss()
			if err := a.startTurn(r, "hm stopped in the middle of your last turn. Check what was done, then continue the task where you left off."); err != nil {
				r.errText = err.Error()
			}
		}),
		ui.Button(r.wid("dismiss"), ui.Text("Dismiss")).Subtle().OnClick(dismiss),
	).Gap(th.Spacing.S).Align(ui.AlignCenter).PaddingXY(th.Spacing.M, th.Spacing.S).
		BorderTop(ui.TokenWarning, th.Stroke.Thick).Background(ui.TokenChromeMuted)
}

// queueView lists messages waiting for the running agent, each removable.
func (a *App) queueView(c *ui.Ctx, r *agentRun) ui.View {
	q := r.queued()
	if len(q) == 0 {
		return nil
	}
	th := c.Theme()
	rows := []ui.View{ui.Caption("Queued; the agent reads these after its current step")}
	for i, m := range q {
		i := i
		rows = append(rows, ui.Row(
			ui.Icon(icons.Clock, 12, th.ForegroundMuted),
			ui.Text(firstLineOf(userShown(m))).Ellipsis(ui.EllipsisEnd).Grow(1),
			ui.IconButton(r.wid(fmt.Sprintf("unqueue-%d", i)), icons.X).Tooltip("Remove").OnClick(func() { r.unqueue(i) }),
		).Gap(th.Spacing.XS).Align(ui.AlignCenter))
	}
	return ui.Column(rows...).Gap(th.Spacing.XXS).PaddingXY(th.Spacing.M, th.Spacing.S).
		BorderTop(ui.TokenBorder, th.Stroke.Thin)
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// replayBar steps through a finished session one message at a time.
func (a *App) replayBar(c *ui.Ctx, r *agentRun) ui.View {
	th := c.Theme()
	n := len(r.session.Snapshot())
	r.replayStep = max(1, min(r.replayStep, n))
	times := r.session.TimesNow()
	label := fmt.Sprintf("Message %d of %d", r.replayStep, n)
	if t := times[r.replayStep-1]; !t.IsZero() {
		label += " · " + t.Format("Jan 2 15:04:05")
		if r.replayStep > 1 && !times[r.replayStep-2].IsZero() {
			label += fmt.Sprintf(" (+%s)", t.Sub(times[r.replayStep-2]).Round(time.Second))
		}
	}
	step := func(d int) func() { return func() { r.replayStep = max(1, min(n, r.replayStep+d)) } }
	return ui.Row(
		ui.IconButton(r.wid("replay-first"), icons.ChevronsLeft).Tooltip("First").Disabled(r.replayStep <= 1).OnClick(step(-n)),
		ui.IconButton(r.wid("replay-prev"), icons.ChevronLeft).Tooltip("Previous").Disabled(r.replayStep <= 1).OnClick(step(-1)),
		ui.IconButton(r.wid("replay-next"), icons.ChevronRight).Tooltip("Next").Disabled(r.replayStep >= n).OnClick(step(1)),
		ui.IconButton(r.wid("replay-last"), icons.ChevronsRight).Tooltip("Last").Disabled(r.replayStep >= n).OnClick(step(n)),
		ui.Caption(label).Grow(1),
		ui.Button(r.wid("replay-exit"), ui.Text("Exit replay")).Subtle().OnClick(func() { r.replay = false }),
	).Gap(th.Spacing.XXS).Align(ui.AlignCenter).PaddingXY(th.Spacing.M, th.Spacing.S).
		BorderTop(ui.TokenAccent, th.Stroke.Thick).Background(ui.TokenChromeMuted)
}

// agentHeader is a session's toolbar. Full screen, usage moves to the footer
// and there is nothing to close: the title bar switches back to the editor.
func (a *App) agentHeader(c *ui.Ctx, r *agentRun, full bool) ui.View {
	th := c.Theme()
	title := r.session.Title
	if title == "" {
		title = "New session"
	}
	items := []ui.View{ui.Strong(title).Ellipsis(ui.EllipsisEnd).Shrink(1)}
	if p, ok := a.agent.projects.Get(r.session.ProjectID); ok {
		items = append(items, ui.Badge(p.Title))
	}
	items = append(items, ui.Spacer())
	if v := a.contextButton(r); v != nil {
		items = append(items, v)
	}
	if !full {
		// A narrow panel cuts the usage along with the title; the buttons stay.
		items = append(items, ui.Caption(a.agentUsage(r)).Ellipsis(ui.EllipsisEnd).Shrink(1).
			Tooltip("Tokens in (cached included) and out, and cost when known"))
	}
	items = append(items,
		ui.IconButton(r.wid("undo"), icons.Undo2).Tooltip("Undo the last agent turn's file changes").
			Disabled(r.lastCheckpoint == nil || r.running).OnClick(func() { a.undoAgentTurn(r) }),
		ui.IconButton(r.wid("replay"), icons.Rewind).Tooltip("Replay the session step by step").
			Disabled(r.running || len(r.session.Snapshot()) == 0).OnClick(func() {
			r.replay = !r.replay
			r.replayStep = 1
		}),
	)
	if !full {
		items = append(items,
			ui.IconButton("agent-history", icons.Clock).Tooltip("Sessions").Menu(a.sessionMenu()),
			ui.IconButton("agent-close", icons.X).Tooltip("Close").OnClick(func() { a.agentOpen = false }))
	}
	return ui.Row(items...).Gap(th.Spacing.XXS).Align(ui.AlignCenter).PaddingXY(th.Spacing.M, th.Spacing.S).BorderBottom(ui.TokenBorder, th.Stroke.Thin)
}

// sessionMenu lists recent sessions for the docked agent, which has no
// sidebar.
func (a *App) sessionMenu() []ui.MenuItem {
	var items []ui.MenuItem
	for i, s := range a.sessionList() {
		if i == 15 {
			break
		}
		id := s.ID
		items = append(items, ui.MenuItem{Label: sessionTitle(s.Title), Shortcut: ago(s.Updated),
			Checked:  a.agentTabIndex(agentTabSession, id) == a.agent.active && a.agentTabIndex(agentTabSession, id) >= 0,
			OnSelect: func() { a.resumeAgentSession(id) }})
	}
	if len(items) == 0 {
		items = append(items, ui.MenuItem{Label: "No saved sessions yet", Disabled: true})
	}
	return append(items, ui.MenuSeparator,
		ui.MenuItem{Label: "Projects", Icon: icons.FolderKanban, OnSelect: func() { a.openAgentPage(agentTabProjects, "") }},
		ui.MenuItem{Label: "Agent Settings", Icon: icons.Settings, OnSelect: func() { a.openAgentPage(agentTabSettings, "") }})
}

func sessionTitle(t string) string {
	if t == "" {
		return "Untitled session"
	}
	return t
}

// agentUsage is the session's tokens in and out, and cost when known.
func (a *App) agentUsage(r *agentRun) string {
	if r == nil {
		return ""
	}
	u := r.session.UsageNow()
	if u.Input+u.Output == 0 {
		return ""
	}
	usage := fmt.Sprintf("↑%s ↓%s", tokens(u.Input+u.CacheRead+u.CacheWrite), tokens(u.Output))
	if u.Cost > 0 {
		usage += fmt.Sprintf(" · $%.3f", u.Cost)
	}
	return usage
}

// agentFullView is the agent in the editor's place: the tab strip, then the
// active tab; a session shows its conversation in a centered column with
// the short composer under it.
func (a *App) agentFullView(c *ui.Ctx) ui.View {
	a.ensureAgentTab()
	t := a.activeAgentTab()
	var body ui.View
	if t.run != nil {
		body = a.sessionFull(c, t.run)
	} else {
		body = a.agentPage(c, t)
	}
	return ui.Column(a.agentTabStrip(c, true), ui.ViewOf(body).Grow(1)).Grow(1).Background(ui.TokenSurface)
}

func (a *App) sessionFull(c *ui.Ctx, r *agentRun) ui.View {
	th := c.Theme()
	var main ui.View
	if a.agentFresh(r) {
		main = a.agentIntro(c)
	} else {
		main = a.transcript(c, r)
	}
	col := []ui.View{ui.ViewOf(main).Grow(1)}
	col = append(col, a.sessionFooter(c, r)...)
	if !r.replay {
		col = append(col, ui.Column(a.composer(c, r, false)).PaddingXY(th.Spacing.S, th.Spacing.M))
	}
	return ui.Column(
		a.agentHeader(c, r, true),
		ui.Row(ui.Column(col...).Grow(1).MaxWidth(agentColumnW)).Justify(ui.JustifyCenter).Align(ui.AlignStretch).Grow(1),
	).Grow(1)
}

// agentColumnW is the widest the full-screen conversation gets.
const agentColumnW = 880

// agentIntro fills a new full-screen session: what the agent can do and how
// to steer it.
func (a *App) agentIntro(c *ui.Ctx) ui.View {
	th := c.Theme()
	tip := func(icon icons.Icon, text string) ui.View {
		return ui.Row(ui.Icon(icon, th.Metrics.IconSizeSM, th.ForegroundMuted), ui.Muted(text).Ellipsis(ui.EllipsisEnd).Shrink(1)).Gap(th.Spacing.S)
	}
	intro := ui.Column(
		ui.Title("Hm"),
		ui.Row(ui.Muted("Ask about this project, or tell the agent what to build or fix.").Ellipsis(ui.EllipsisEnd).Shrink(1)),
		ui.Column(
			tip(icons.AtSign, "@file, @folder, @selection, @diagnostics or @git-diff add context"),
			tip(icons.SlidersHorizontal, "/mode switches between lazy, rubber-duck, auto and fullstack"),
			tip(icons.FolderKanban, "Pick a project below to give the agent its context"),
			tip(icons.MessageSquarePlus, "Type while the agent works; it reads your message after its next step"),
			tip(icons.SquareCode, "The title bar button, or "+a.key("agent.toggle")+", goes back to the editor"),
		).Gap(th.Spacing.S).MarginXY(0, th.Spacing.M),
	).Gap(th.Spacing.S).Shrink(1)
	// Centered by spacers rather than ui.Center, so a narrow pane shrinks
	// the block and cuts its lines short instead of spilling past the pane.
	return ui.Column(ui.Row(ui.Spacer(), intro, ui.Spacer())).Justify(ui.JustifyCenter).Grow(1)
}

// agentStatus is the footer's agent part, full screen: mode, model, usage.
func (a *App) agentStatus(c *ui.Ctx) []ui.View {
	th := c.Theme()
	r := a.run()
	if r == nil {
		return nil
	}
	icon, ok := modeIcons[r.mode]
	if !ok {
		icon = icons.Bot
	}
	out := []ui.View{
		ui.Row(ui.Icon(icon, 14, th.ForegroundMuted), ui.Caption(r.mode)).Gap(th.Spacing.XXS).Shrink(0).Tooltip("Mode · /mode to switch"),
	}
	if m := a.modelOf(r.provider); m != "" {
		out = append(out, ui.Caption(m).Ellipsis(ui.EllipsisEnd).Shrink(1).Tooltip(providerLabel(r.provider)))
	}
	if u := a.agentUsage(r); u != "" {
		out = append(out, ui.Caption(u).Ellipsis(ui.EllipsisEnd).Shrink(1).Tooltip("Tokens in (cached included) and out, and cost when known"))
	}
	return out
}

func tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// ago says how long ago t was, coarsely.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("Jan 2")
}

// transcript shows the session's messages, then the turn in progress.
func (a *App) transcript(c *ui.Ctx, r *agentRun) ui.View {
	th := c.Theme()
	st := r
	var rows []ui.View
	msgs := r.session.Snapshot()
	if r.replay && r.replayStep < len(msgs) {
		msgs = msgs[:max(1, r.replayStep)]
	}
	results := map[string]provider.Block{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == provider.ToolResult {
				results[b.ToolID] = b
			}
		}
	}
	for i, m := range msgs {
		if m.Role == provider.User {
			if t := m.TextOf(); t != "" {
				rows = append(rows, ui.Column(ui.Paragraph(userShown(t)).Selectable(r.wid(fmt.Sprintf("user-%d", i)))).
					Padding(th.Spacing.S).Radius(th.Radius.Medium).Background(ui.TokenListActive))
			}
			continue
		}
		for j, b := range m.Blocks {
			switch b.Type {
			case provider.Thinking:
				if strings.TrimSpace(b.Text) != "" {
					rows = append(rows, a.thinkingView(c, r, fmt.Sprintf("think-%d-%d", i, j), b.Text))
				}
			case provider.Text:
				if strings.TrimSpace(b.Text) != "" {
					rows = append(rows, a.agentMarkdown(r.wid(fmt.Sprintf("msg-%d-%d", i, j)), strings.TrimSpace(b.Text)))
				}
			case provider.ToolUse:
				res, done := results[b.ToolID]
				rows = append(rows, a.toolCard(c, r, b, res, done))
			}
		}
	}
	if st.running {
		if st.thinking != "" {
			rows = append(rows, a.thinkingView(c, r, "think-live", st.thinking))
		}
		if st.stream != "" {
			rows = append(rows, a.agentMarkdown(r.wid("msg-live"), st.stream))
		}
		status := "Thinking…"
		if st.streamTool != "" {
			status = "Calling " + st.streamTool + "…"
		}
		if st.approval != nil {
			status = "Waiting for you"
		}
		rows = append(rows, ui.Row(ui.Spinner(r.wid("spin"), 14), ui.Caption(status)).Gap(th.Spacing.XS))
		c.Animate(120e6)
	}
	if len(rows) == 0 {
		return ui.ViewOf(ui.EmptyState("Ask the agent", "Mention @file, @folder, @selection, @diagnostics or @git-diff "+
			"to add context. /mode switches mode.").EmptyIcon(icons.Bot)).Grow(1)
	}
	return ui.Scroll(r.wid("transcript"), ui.Column(rows...).Gap(th.Spacing.S).Padding(th.Spacing.M)).Follow(!r.replay).Grow(1)
}

// agentMarkdown renders assistant text; links resolve against the workspace.
func (a *App) agentMarkdown(id, text string) ui.View {
	return ui.Markdown(id, text).OnLink(func(u string) { a.openLink(a.ws.Root, u) })
}

// userShown hides attached context, which can be long, from the bubble.
func userShown(t string) string {
	for _, tag := range []string{"\n\n<file", "\n\n<folder", "\n\n<selection", "\n\n<diagnostics", "\n\nRun git_diff"} {
		if i := strings.Index(t, tag); i >= 0 {
			return t[:i] + "  [+ context]"
		}
	}
	return t
}

func (a *App) thinkingView(c *ui.Ctx, r *agentRun, id, text string) ui.View {
	th := c.Theme()
	open := r.calls[id].open
	icon := icons.ChevronRight
	if open {
		icon = icons.ChevronDown
	}
	rows := []ui.View{ui.Button(r.wid(id), ui.Row(ui.Icon(icon, 12, th.ForegroundMuted), ui.Caption("Reasoning")).Gap(th.Spacing.XXS)).
		Ghost().OnClick(func() { cv := r.calls[id]; cv.open = !cv.open; r.calls[id] = cv })}
	if open {
		rows = append(rows, ui.Paragraph(strings.TrimSpace(text)).Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted)).
			Selectable(r.wid(id+"-text")))
	}
	return ui.Column(rows...).Gap(th.Spacing.XXS)
}

func (a *App) toolCard(c *ui.Ctx, r *agentRun, call, res provider.Block, done bool) ui.View {
	th := c.Theme()
	st := r
	cv := st.calls[call.ToolID]
	summary := cv.summary
	if summary == "" {
		summary = string(call.Input)
		if len(summary) > 120 {
			summary = summary[:120] + "…"
		}
	}
	icon, color := icons.Wrench, th.ForegroundMuted
	switch {
	case !done:
		icon = icons.LoaderCircle
	case res.IsError:
		icon, color = icons.CircleX, th.ErrorForeground
	default:
		icon, color = icons.CircleCheck, th.SuccessForeground
	}
	id := r.wid("tool-" + call.ToolID)
	head := ui.Button(id, ui.Row(
		ui.Icon(icon, 14, color),
		ui.Caption(call.ToolName),
		ui.Text(summary).Ellipsis(ui.EllipsisEnd).Grow(1),
	).Gap(th.Spacing.XS).Grow(1)).Ghost().HoverFill().Grow(1).
		OnClick(func() { cv := st.calls[call.ToolID]; cv.open = !cv.open; st.calls[call.ToolID] = cv })
	copyBtn := ui.IconButton(id+"-copy", icons.Copy).Tooltip("Copy call and result").OnClick(func() {
		text := call.ToolName + " " + string(call.Input)
		if done {
			text += "\n\n" + res.Text
		}
		if clip := c.Clipboard(); clip != nil {
			clip.Set(text)
		}
	})
	rows := []ui.View{ui.Row(head, copyBtn).Gap(th.Spacing.XXS).Align(ui.AlignCenter)}
	if call.ToolName == "task" {
		if v := a.subagentRow(c, r, call, res, done); v != nil {
			rows = append(rows, v)
		}
	}
	if cv.open && done {
		text := res.Text
		if lines := strings.Split(text, "\n"); len(lines) > maxResultLines {
			text = strings.Join(lines[:maxResultLines], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-maxResultLines)
		}
		rows = append(rows, ui.Column(ui.Paragraph(text).Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted)).
			Selectable(id+"-result")).
			Padding(th.Spacing.S).Radius(th.Radius.Small).Background(ui.TokenSurface))
	}
	return ui.Column(rows...).Gap(th.Spacing.XXS).Radius(th.Radius.Medium).Border(ui.TokenBorder, th.Stroke.Thin).
		Padding(th.Spacing.XXS)
}

// subagentRow shows what a task call's sub-agent is doing, with a button
// to open its session.
func (a *App) subagentRow(c *ui.Ctx, r *agentRun, call, res provider.Block, done bool) ui.View {
	th := c.Theme()
	ch := r.children[call.ToolID]
	id := ""
	if ch != nil {
		id = ch.session.ID
	} else if m := subagentID.FindStringSubmatch(res.Text); m != nil {
		id = m[1] // a saved session, from before this run of hm
	}
	if id == "" {
		return nil
	}
	status := "Finished"
	var lead ui.View = ui.Icon(icons.Bot, 12, th.ForegroundMuted)
	if ch != nil && ch.running {
		lead = ui.Spinner(r.wid("sub-spin-"+call.ToolID), 12)
		status = "Thinking…"
		switch {
		case ch.approval != nil || ch.question != nil:
			status = "Waiting for you"
		case ch.streamTool != "":
			status = "Calling " + ch.streamTool + "…"
		case ch.stream != "":
			status = lastLine(ch.stream)
		default:
			if t := lastToolSummary(ch); t != "" {
				status = t
			}
		}
	} else if !done {
		status = "Starting…"
	}
	return ui.Row(
		lead,
		ui.Caption(status).Ellipsis(ui.EllipsisEnd).Grow(1).Shrink(1),
		ui.Button(r.wid("sub-open-"+call.ToolID), ui.Text("Open")).Subtle().
			Tooltip("Open the sub-agent's session in a tab").OnClick(func() { a.resumeAgentSession(id) }),
	).Gap(th.Spacing.XS).Align(ui.AlignCenter).PaddingXY(th.Spacing.S, 0)
}

// subagentID finds the session a task result names.
var subagentID = regexp.MustCompile(`\(sub-agent session ([0-9a-f-]+)\)`)

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// lastToolSummary is what the sub-agent's latest call does.
func lastToolSummary(r *agentRun) string {
	msgs := r.session.Snapshot()
	for i := len(msgs) - 1; i >= 0; i-- {
		uses := msgs[i].ToolUses()
		if len(uses) == 0 {
			continue
		}
		u := uses[len(uses)-1]
		if cv := r.calls[u.ToolID]; cv.summary != "" {
			return u.ToolName + " " + cv.summary
		}
		return u.ToolName
	}
	return ""
}

func (a *App) todoView(c *ui.Ctx, r *agentRun) ui.View {
	th := c.Theme()
	items := r.todos.Items()
	if len(items) == 0 {
		return nil
	}
	rows := []ui.View{ui.Caption("Tasks")}
	for _, it := range items {
		icon, color := icons.Circle, th.ForegroundMuted
		switch it.Status {
		case "in_progress":
			icon, color = icons.CircleDot, th.Accent
		case "done":
			icon, color = icons.CircleCheck, th.SuccessForeground
		}
		rows = append(rows, ui.Row(ui.Icon(icon, 12, color), ui.Text(it.Text).Ellipsis(ui.EllipsisEnd).Grow(1)).Gap(th.Spacing.XS))
	}
	return ui.Column(rows...).Gap(th.Spacing.XXS).PaddingXY(th.Spacing.M, th.Spacing.S).
		BorderTop(ui.TokenBorder, th.Stroke.Thin)
}

// approvalCard asks before a call runs; for edits it shows each hunk with a
// box to keep or drop it.
func (a *App) approvalCard(c *ui.Ctx, r *agentRun, p *pendingApproval) ui.View {
	th := c.Theme()
	req := p.req
	title := "Run " + req.Tool + "?"
	if len(p.files) > 0 {
		title = "Apply these changes?"
	}
	rows := []ui.View{ui.Strong(title)}
	if req.Reason != "" {
		rows = append(rows, ui.Paragraph("Asking because "+req.Reason).Size(th.Typography.Caption.Size).Style(ui.Spec{}.TextColor(ui.TokenWarning)))
	}
	if len(p.files) == 0 {
		rows = append(rows, ui.Column(ui.Paragraph(req.Plan.Summary).Style(ui.Spec{}.FontWeight(600))).
			Padding(th.Spacing.S).Radius(th.Radius.Small).Background(ui.TokenSurface))
	}
	var hunks []ui.View
	for fi := range p.files {
		f := &p.files[fi]
		label := a.ws.Rel(f.edit.Path)
		switch {
		case f.edit.Old == nil:
			label += " (new file)"
		case f.edit.New == nil:
			label += " (delete)"
		}
		hunks = append(hunks, ui.Strong(label))
		for hi, h := range f.hunks {
			hi, fi := hi, fi
			hunks = append(hunks, ui.Row(
				ui.Checkbox(r.wid(fmt.Sprintf("hunk-%d-%d", fi, hi)), fmt.Sprintf("Lines %d–%d", h.B0+1, max(h.B1, h.B0+1))).
					Check(f.accepted[hi]).OnToggle(func(v bool) { p.files[fi].accepted[hi] = v }),
			))
			hunks = append(hunks, hunkView(c, f, h))
		}
	}
	if len(hunks) > 0 {
		rows = append(rows, ui.Scroll(r.wid("approval-hunks"), ui.Column(hunks...).Gap(th.Spacing.XS)).Height(280))
	}
	rows = append(rows, ui.TextField(r.wid("approval-note"), p.note).Placeholder("Note for the agent (optional)").
		OnChange(func(v string) { p.note = v }))
	actions := []ui.View{
		ui.Button(r.wid("approval-deny"), ui.Text("Deny")).OnClick(func() { a.answerApproval(r, false, false) }),
		ui.Spacer(),
	}
	if req.CanGrant && len(p.files) == 0 {
		actions = append(actions, ui.Button(r.wid("approval-session"), ui.Text("Allow for session")).
			Tooltip("Allow calls like this until the session ends").OnClick(func() { a.answerApproval(r, true, true) }))
	}
	allow := "Allow once"
	if len(p.files) > 0 {
		allow = "Apply selected"
	}
	actions = append(actions, ui.Button(r.wid("approval-allow"), ui.Text(allow)).Primary().OnClick(func() { a.answerApproval(r, true, false) }))
	// A narrow panel wraps the buttons rather than cut them off.
	rows = append(rows, ui.Row(actions...).Gap(th.Spacing.XS).Wrap())
	return ui.Column(rows...).Gap(th.Spacing.S).Padding(th.Spacing.M).
		BorderTop(ui.TokenAccent, th.Stroke.Thick).Background(ui.TokenChromeMuted)
}

// hunkView shows one hunk as removed and added lines.
func hunkView(c *ui.Ctx, f *reviewFile, h diff.Hunk) ui.View {
	th := c.Theme()
	var lines []ui.View
	add := func(prefix, text string, tok ui.Token) {
		lines = append(lines, ui.Text(prefix+text).Ellipsis(ui.EllipsisEnd).Style(ui.Spec{}.TextColor(tok)))
	}
	const maxLines = 30
	n := 0
	for _, l := range f.a[h.A0:h.A1] {
		if n++; n > maxLines {
			break
		}
		add("- ", l, ui.TokenError)
	}
	for _, l := range f.b[h.B0:h.B1] {
		if n++; n > maxLines {
			break
		}
		add("+ ", l, ui.TokenSuccess)
	}
	if n > maxLines {
		lines = append(lines, ui.Caption(fmt.Sprintf("… %d more lines", (h.A1-h.A0)+(h.B1-h.B0)-maxLines)))
	}
	return ui.Column(lines...).Padding(th.Spacing.XS).Radius(th.Radius.Small).Background(ui.TokenSurface)
}

func (a *App) questionCard(c *ui.Ctx, r *agentRun, q *pendingQuestion) ui.View {
	th := c.Theme()
	send := func() {
		if strings.TrimSpace(q.answer) == "" {
			return
		}
		q.reply <- q.answer
		r.question = nil
	}
	return ui.Column(
		ui.Strong("The agent asks"),
		ui.Paragraph(q.text),
		ui.Row(
			ui.TextField(r.wid("question-answer"), q.answer).Placeholder("Your answer").DefaultFocus().
				OnChange(func(v string) { q.answer = v }).OnSubmit(func(string) { send() }).Grow(1),
			ui.Button(r.wid("question-send"), ui.Text("Answer")).Primary().OnClick(send),
		).Gap(th.Spacing.XS),
	).Gap(th.Spacing.S).Padding(th.Spacing.M).BorderTop(ui.TokenAccent, th.Stroke.Thick).Background(ui.TokenChromeMuted)
}

// agentInput is a session's message box.
func agentInput(c *ui.Ctx, r *agentRun) *ui.TextInput {
	return c.Widget(r.wid("input"), func() any { return ui.NewTextInput(ui.TextFieldConfig{Multiline: true}) }).(*ui.TextInput)
}

// composer is the message box with the mode, model and project pickers
// under it. A fresh session shows it tall; once the chat starts it shrinks
// to grow with its text. While the agent works, what is sent waits in the
// queue.
func (a *App) composer(c *ui.Ctx, r *agentRun, fresh bool) ui.View {
	th := c.Theme()
	minRows, maxRows := 1, 8
	if fresh {
		minRows, maxRows = 5, 14
	}
	// The box shows focus, not the field inside it.
	tf := agentInput(c, r)
	border := ui.TokenBorder
	if tf.Focused() {
		border = ui.TokenFocusRing
		// The panel may open over a few frames before the field is laid
		// out, so keep asking for focus until it lands.
		r.focusInput = false
	}
	placeholder := "Plan, build, / for commands, @ for context"
	if r.running {
		placeholder = "Add guidance; the agent reads it after its current step"
	}
	field := ui.TextArea(r.wid("input"), r.input).Rows(minRows, maxRows).
		Placeholder(placeholder).
		OnChange(func(v string) { r.input = v }).
		OnSubmit(func(string) { a.sendRun(r) }).
		Suggest(a.agentSuggest).
		DefaultFocus().FocusNow(r.focusInput).
		Style(ui.Background(ui.TokenUnset).Border(ui.TokenUnset, 0).When(ui.Focused, ui.Spec{}.Border(ui.TokenUnset, 0)))
	send := ui.Button(r.wid("send"), ui.Icon(icons.ArrowUp, th.Metrics.IconSizeSM, th.AccentForeground)).Primary().
		Tooltip("Send (Enter)").Disabled(strings.TrimSpace(r.input) == "").OnClick(func() { a.sendRun(r) })
	controls := []ui.View{a.modePicker(r), a.composerModel(r), a.projectPicker(r), ui.Spacer()}
	if r.running {
		send = send.Tooltip("Queue for the agent (Enter)")
		controls = append(controls, ui.IconButton(r.wid("stop"), icons.Square).Tooltip("Stop").OnClick(func() { a.stopRun(r) }))
	}
	controls = append(controls, send)
	return ui.Column(
		field,
		ui.Row(controls...).Gap(th.Spacing.XS).PaddingXY(th.Spacing.XS, 0),
	).Gap(th.Spacing.XXS).PaddingXY(0, th.Spacing.XS).Radius(th.Radius.XLarge).
		Border(border, th.Stroke.Thin).Background(ui.TokenSurface)
}

// modeIcons marks the built-in modes in the mode picker.
var modeIcons = map[string]icons.Icon{
	"lazy":        icons.Coffee,
	"rubber-duck": icons.MessageCircle,
	"auto":        icons.Sparkles,
	"fullstack":   icons.Layers,
}

func (a *App) modePicker(r *agentRun) ui.View {
	modes := agent.ModeNames(a.modes())
	items := make([]ui.MenuItem, len(modes))
	for i, m := range modes {
		m := m
		items[i] = ui.MenuItem{Label: m, Checked: m == r.mode, OnSelect: func() { r.mode = m }}
	}
	icon, ok := modeIcons[r.mode]
	if !ok {
		icon = icons.Bot
	}
	return ui.MenuButton(r.wid("mode"), r.mode, items).IconStart(icon).Subtle().Disabled(r.running)
}

// composerModel switches between the configured providers, each shown by the
// model it runs; the model itself is picked in Settings.
func (a *App) composerModel(r *agentRun) ui.View {
	var items []ui.MenuItem
	for _, p := range a.providerNames() {
		p := p
		label, hint := a.modelOf(p), providerLabel(p)
		if label == "" {
			label = providerLabel(p)
			hint = ""
		}
		if !a.providerReady(p) {
			hint = "no key"
		}
		items = append(items, ui.MenuItem{Label: label, Shortcut: hint, Checked: p == r.provider,
			OnSelect: func() { r.provider = p }})
	}
	items = append(items, ui.MenuSeparator, ui.MenuItem{Label: "Models and keys…", OnSelect: a.openSettings})
	label := a.modelOf(r.provider)
	switch {
	case !a.providerReady(r.provider):
		label = "Set up a model"
	case label == "":
		label = providerLabel(r.provider)
	}
	return ui.MenuButton(r.wid("model"), label, items).Ghost().Shrink(1).Disabled(r.running)
}

// projectPicker picks the project whose context the session starts with.
// The context is part of the system prompt, so it is fixed once the
// session has a message.
func (a *App) projectPicker(r *agentRun) ui.View {
	s := r.session
	items := []ui.MenuItem{{Label: "No project", Checked: s.ProjectID == "", OnSelect: func() { s.ProjectID = "" }}}
	for _, p := range a.agent.projects.List() {
		id := p.ID
		items = append(items, ui.MenuItem{Label: p.Title, Checked: id == s.ProjectID, OnSelect: func() { s.ProjectID = id }})
	}
	items = append(items, ui.MenuSeparator,
		ui.MenuItem{Label: "Manage projects…", OnSelect: func() { a.openAgentPage(agentTabProjects, "") }})
	label := "No project"
	if p, ok := a.agent.projects.Get(s.ProjectID); ok {
		label = p.Title
	}
	return ui.MenuButton(r.wid("project"), label, items).IconStart(icons.FolderKanban).Ghost().Shrink(1).
		Disabled(r.running || len(s.Snapshot()) > 0)
}

// agentSuggest completes slash commands, mode names and @ mentions.
func (a *App) agentSuggest(value string, caret int) ([]ui.Suggestion, int, int) {
	before := value[:min(caret, len(value))]
	start := strings.LastIndexAny(before, " \n") + 1
	word := before[start:]
	var out []ui.Suggestion
	switch {
	case strings.HasPrefix(value, "/mode ") && start == len("/mode "):
		for _, m := range agent.ModeNames(a.modes()) {
			if strings.HasPrefix(m, word) {
				out = append(out, ui.Suggestion{Label: m, Detail: "mode"})
			}
		}
	case strings.HasPrefix(value, "/provider ") && start == len("/provider "):
		for _, p := range a.providerNames() {
			if strings.HasPrefix(p, word) {
				out = append(out, ui.Suggestion{Label: p, Detail: "provider"})
			}
		}
	case start == 0 && strings.HasPrefix(word, "/"):
		for _, cmd := range []string{"/mode ", "/provider ", "/context", "/new"} {
			if strings.HasPrefix(cmd, word) {
				out = append(out, ui.Suggestion{Label: cmd})
			}
		}
	case strings.HasPrefix(word, "@"):
		q := strings.ToLower(word[1:])
		for _, s := range []string{"selection", "diagnostics", "git-diff"} {
			if strings.HasPrefix(s, q) {
				out = append(out, ui.Suggestion{Label: "@" + s, Detail: "context"})
			}
		}
		if a.idx != nil {
			for _, p := range a.idx.files {
				rel := a.ws.Rel(p)
				if strings.Contains(strings.ToLower(rel), q) {
					out = append(out, ui.Suggestion{Label: "@" + rel, Detail: "file"})
					if len(out) >= 20 {
						break
					}
				}
			}
		}
	}
	return out, start, caret
}
