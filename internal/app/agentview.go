package app

import (
	"fmt"
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

func (a *App) agentView(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	body := []ui.View{a.agentHeader(c, false)}
	switch {
	case st.sessionsOpen:
		body = append(body, a.sessionList(c))
	case a.agentFresh():
		// Before the first message the composer sits at the top, tall.
		body = append(body, ui.Column(a.composer(c, true), ui.Caption(
			"Enter to send · Shift+Enter for a new line")).
			Gap(th.Spacing.S).Padding(th.Spacing.M))
		if st.errText != "" {
			body = append(body, a.agentError(c))
		}
		body = append(body, ui.Spacer())
		return ui.Column(body...).Grow(1).Background(ui.TokenChrome).BorderLeft(ui.TokenBorder, th.Stroke.Thin)
	default:
		body = append(body, a.transcript(c))
	}
	if t := a.todoView(c); t != nil {
		body = append(body, t)
	}
	if p := st.approval; p != nil {
		body = append(body, a.approvalCard(c, p))
	}
	if q := st.question; q != nil {
		body = append(body, a.questionCard(c, q))
	}
	if st.errText != "" {
		body = append(body, a.agentError(c))
	}
	body = append(body, ui.Column(a.composer(c, false)).Padding(th.Spacing.S))
	return ui.Column(body...).Grow(1).Background(ui.TokenChrome).BorderLeft(ui.TokenBorder, th.Stroke.Thin)
}

// agentFresh reports whether the session has nothing in it yet.
func (a *App) agentFresh() bool {
	st := &a.agent
	return !st.running && (st.session == nil || len(st.session.Snapshot()) == 0)
}

func (a *App) agentError(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	return ui.Row(
		ui.Paragraph(st.errText).Style(ui.Spec{}.TextColor(ui.TokenError)).Grow(1),
		ui.IconButton("agent-err-close", icons.X).OnClick(func() { st.errText = "" }),
	).Gap(th.Spacing.XS).PaddingXY(th.Spacing.M, th.Spacing.XXS)
}

// agentHeader is the agent's toolbar. Full screen, usage moves to the footer
// and there is nothing to close: the title bar switches back to the editor.
func (a *App) agentHeader(c *ui.Ctx, full bool) ui.View {
	th := c.Theme()
	st := &a.agent
	title := "Agent"
	if full && st.session != nil && st.session.Title != "" {
		title = st.session.Title
	}
	items := []ui.View{ui.Strong(title).Ellipsis(ui.EllipsisEnd).Shrink(1), ui.Spacer()}
	if !full {
		items = append(items, ui.Caption(a.agentUsage()).Tooltip("Tokens in (cached included) and out, and cost when known"))
	}
	items = append(items,
		ui.IconButton("agent-undo", icons.Undo2).Tooltip("Undo the last agent turn's file changes").
			Disabled(st.lastCheckpoint == nil || st.running).OnClick(a.undoAgentTurn),
		ui.IconButton("agent-history", icons.Clock).Tooltip("Sessions").
			OnClick(a.toggleSessions),
		ui.IconButton("agent-new", icons.SquarePen).Tooltip("New session").Disabled(st.running).OnClick(a.newAgentSession),
	)
	if !full {
		items = append(items, ui.IconButton("agent-close", icons.X).Tooltip("Close").OnClick(func() { a.agentOpen = false }))
	}
	return ui.Row(items...).Gap(th.Spacing.XXS).PaddingXY(th.Spacing.M, th.Spacing.S).BorderBottom(ui.TokenBorder, th.Stroke.Thin)
}

// agentUsage is the session's tokens in and out, and cost when known.
func (a *App) agentUsage() string {
	s := a.agent.session
	if s == nil || s.UsageNow().Input+s.UsageNow().Output == 0 {
		return ""
	}
	u := s.UsageNow()
	usage := fmt.Sprintf("↑%s ↓%s", tokens(u.Input+u.CacheRead+u.CacheWrite), tokens(u.Output))
	if u.Cost > 0 {
		usage += fmt.Sprintf(" · $%.3f", u.Cost)
	}
	return usage
}

// agentFullView is the agent in the editor's place: the conversation in a
// centered column, the short composer under it.
func (a *App) agentFullView(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	var main ui.View
	switch {
	case st.sessionsOpen:
		main = a.sessionList(c)
	case a.agentFresh():
		main = a.agentIntro(c)
	default:
		main = a.transcript(c)
	}
	col := []ui.View{ui.ViewOf(main).Grow(1)}
	if t := a.todoView(c); t != nil {
		col = append(col, t)
	}
	if p := st.approval; p != nil {
		col = append(col, a.approvalCard(c, p))
	}
	if q := st.question; q != nil {
		col = append(col, a.questionCard(c, q))
	}
	if st.errText != "" {
		col = append(col, a.agentError(c))
	}
	col = append(col, ui.Column(a.composer(c, false)).PaddingXY(th.Spacing.S, th.Spacing.M))
	return ui.Column(
		a.agentHeader(c, true),
		ui.Row(ui.Column(col...).Grow(1).MaxWidth(agentColumnW)).Justify(ui.JustifyCenter).Align(ui.AlignStretch).Grow(1),
	).Grow(1).Background(ui.TokenSurface)
}

// agentColumnW is the widest the full-screen conversation gets.
const agentColumnW = 880

// agentIntro fills a new full-screen session: what the agent can do and how
// to steer it.
func (a *App) agentIntro(c *ui.Ctx) ui.View {
	th := c.Theme()
	tip := func(icon icons.Icon, text string) ui.View {
		return ui.Row(ui.Icon(icon, th.Metrics.IconSizeSM, th.ForegroundMuted), ui.Muted(text)).Gap(th.Spacing.S)
	}
	return ui.Center(ui.Column(
		ui.Title("Hm"),
		ui.Muted("Ask about this project, or tell the agent what to build or fix."),
		ui.Column(
			tip(icons.AtSign, "@file, @folder, @selection, @diagnostics or @git-diff add context"),
			tip(icons.SlidersHorizontal, "/mode switches between lazy, rubber-duck, auto and fullstack"),
			tip(icons.Clock, "Earlier sessions are under the clock above"),
			tip(icons.SquareCode, "The title bar button, or "+a.key("agent.toggle")+", goes back to the editor"),
		).Gap(th.Spacing.S).MarginXY(0, th.Spacing.M),
	).Gap(th.Spacing.S).Align(ui.AlignStart))
}

// agentStatus is the footer's agent part, full screen: mode, model, usage.
func (a *App) agentStatus(c *ui.Ctx) []ui.View {
	th := c.Theme()
	st := &a.agent
	icon, ok := modeIcons[st.mode]
	if !ok {
		icon = icons.Bot
	}
	out := []ui.View{
		ui.Row(ui.Icon(icon, 14, th.ForegroundMuted), ui.Caption(st.mode)).Gap(th.Spacing.XXS).Tooltip("Mode · /mode to switch"),
	}
	if m := a.modelOf(st.provider); m != "" {
		out = append(out, ui.Caption(m).Tooltip(providerLabel(st.provider)))
	}
	if u := a.agentUsage(); u != "" {
		out = append(out, ui.Caption(u).Tooltip("Tokens in (cached included) and out, and cost when known"))
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

// toggleSessions opens or closes the history list, reading it on open.
func (a *App) toggleSessions() {
	st := &a.agent
	st.sessionsOpen = !st.sessionsOpen
	if !st.sessionsOpen {
		st.focusInput = true
		return
	}
	st.sessions = st.sessions[:0]
	for _, s := range st.store.List() {
		if s.Turns > 0 {
			st.sessions = append(st.sessions, s)
		}
	}
}

func (a *App) sessionList(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	rows := []ui.View{ui.Row(
		ui.Strong("Sessions"),
		ui.Spacer(),
		ui.Caption(fmt.Sprintf("%d in this workspace", len(st.sessions))),
	).Gap(th.Spacing.XS)}
	for i, s := range st.sessions {
		rows = append(rows, a.sessionCard(c, i, s))
	}
	if len(st.sessions) == 0 {
		rows = append(rows, ui.Muted("No saved sessions yet"))
	}
	return ui.Scroll("agent-sessions", ui.Column(rows...).Gap(th.Spacing.S).Padding(th.Spacing.M)).Grow(1)
}

// sessionCard is one saved session: its title, then its mode, size and age.
func (a *App) sessionCard(c *ui.Ctx, i int, s agent.Summary) ui.View {
	th := c.Theme()
	st := &a.agent
	title := s.Title
	if title == "" {
		title = "Untitled session"
	}
	icon, ok := modeIcons[s.Mode]
	if !ok {
		icon = icons.Bot
	}
	turns := "1 message"
	if s.Turns != 1 {
		turns = fmt.Sprintf("%d messages", s.Turns)
	}
	meta := []ui.View{
		ui.Icon(icon, th.Metrics.IconSizeSM, th.ForegroundMuted),
		ui.Caption(s.Mode + " · " + turns),
	}
	if s.Cost >= 0.005 {
		meta = append(meta, ui.Caption(fmt.Sprintf("· $%.2f", s.Cost)))
	}
	meta = append(meta, ui.Spacer(), ui.Caption(ago(s.Updated)).Tooltip(s.Updated.Format("Mon Jan 2 2006, 15:04")))
	border := ui.TokenBorder
	if cur := st.session; cur != nil && cur.ID == s.ID {
		border = ui.TokenAccent
	}
	// A button is one control high unless told otherwise; the card has two
	// lines.
	h := 2*th.Spacing.S + th.Typography.Body.LineHeight + th.Spacing.XS + th.Metrics.IconSizeSM + th.Spacing.XXS
	return ui.Button(fmt.Sprintf("agent-sess-%d", i), ui.Column(
		ui.Text(title).Weight(600).Ellipsis(ui.EllipsisEnd),
		ui.Row(meta...).Gap(th.Spacing.XS),
	).Gap(th.Spacing.XS).Grow(1)).Ghost().HoverFill().
		Height(h).Padding(th.Spacing.S).Radius(th.Radius.XLarge).Border(border, th.Stroke.Thin).Background(ui.TokenSurface).
		OnClick(func() { a.resumeAgentSession(s.ID) })
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
func (a *App) transcript(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	var rows []ui.View
	var msgs []provider.Message
	if st.session != nil {
		msgs = st.session.Snapshot()
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
				rows = append(rows, ui.Column(ui.Paragraph(userShown(t))).
					Padding(th.Spacing.S).Radius(th.Radius.Medium).Background(ui.TokenListActive))
			}
			continue
		}
		for j, b := range m.Blocks {
			switch b.Type {
			case provider.Thinking:
				if strings.TrimSpace(b.Text) != "" {
					rows = append(rows, a.thinkingView(c, fmt.Sprintf("think-%d-%d", i, j), b.Text))
				}
			case provider.Text:
				if strings.TrimSpace(b.Text) != "" {
					rows = append(rows, a.agentMarkdown(fmt.Sprintf("msg-%d-%d", i, j), strings.TrimSpace(b.Text)))
				}
			case provider.ToolUse:
				res, done := results[b.ToolID]
				rows = append(rows, a.toolCard(c, b, res, done))
			}
		}
	}
	if st.running {
		if st.thinking != "" {
			rows = append(rows, a.thinkingView(c, "think-live", st.thinking))
		}
		if st.stream != "" {
			rows = append(rows, a.agentMarkdown("msg-live", st.stream))
		}
		status := "Thinking…"
		if st.streamTool != "" {
			status = "Calling " + st.streamTool + "…"
		}
		if st.approval != nil {
			status = "Waiting for you"
		}
		rows = append(rows, ui.Row(ui.Spinner("agent-spin", 14), ui.Caption(status)).Gap(th.Spacing.XS))
		c.Animate(120e6)
	}
	if len(rows) == 0 {
		return ui.ViewOf(ui.EmptyState("Ask the agent", "Mention @file, @folder, @selection, @diagnostics or @git-diff "+
			"to add context. /mode switches mode.").EmptyIcon(icons.Bot)).Grow(1)
	}
	return ui.Scroll("agent-transcript", ui.Column(rows...).Gap(th.Spacing.S).Padding(th.Spacing.M)).Follow(true).Grow(1)
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

func (a *App) thinkingView(c *ui.Ctx, id, text string) ui.View {
	th := c.Theme()
	open := a.agent.calls[id].open
	icon := icons.ChevronRight
	if open {
		icon = icons.ChevronDown
	}
	rows := []ui.View{ui.Button(id, ui.Row(ui.Icon(icon, 12, th.ForegroundMuted), ui.Caption("Reasoning")).Gap(th.Spacing.XXS)).
		Ghost().OnClick(func() { cv := a.agent.calls[id]; cv.open = !cv.open; a.agent.calls[id] = cv })}
	if open {
		rows = append(rows, ui.Paragraph(strings.TrimSpace(text)).Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted)))
	}
	return ui.Column(rows...).Gap(th.Spacing.XXS)
}

func (a *App) toolCard(c *ui.Ctx, call, res provider.Block, done bool) ui.View {
	th := c.Theme()
	st := &a.agent
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
	id := "tool-" + call.ToolID
	head := ui.Button(id, ui.Row(
		ui.Icon(icon, 14, color),
		ui.Caption(call.ToolName),
		ui.Text(summary).Ellipsis(ui.EllipsisEnd).Grow(1),
	).Gap(th.Spacing.XS).Grow(1)).Ghost().HoverFill().Grow(1).
		OnClick(func() { cv := st.calls[call.ToolID]; cv.open = !cv.open; st.calls[call.ToolID] = cv })
	rows := []ui.View{head}
	if cv.open && done {
		text := res.Text
		if lines := strings.Split(text, "\n"); len(lines) > maxResultLines {
			text = strings.Join(lines[:maxResultLines], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-maxResultLines)
		}
		rows = append(rows, ui.Column(ui.Paragraph(text).Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted))).
			Padding(th.Spacing.S).Radius(th.Radius.Small).Background(ui.TokenSurface))
	}
	return ui.Column(rows...).Gap(th.Spacing.XXS).Radius(th.Radius.Medium).Border(ui.TokenBorder, th.Stroke.Thin).
		Padding(th.Spacing.XXS)
}

func (a *App) todoView(c *ui.Ctx) ui.View {
	th := c.Theme()
	items := a.agent.todos.Items()
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
func (a *App) approvalCard(c *ui.Ctx, p *pendingApproval) ui.View {
	th := c.Theme()
	req := p.req
	title := "Run " + req.Tool + "?"
	if len(p.files) > 0 {
		title = "Apply these changes?"
	}
	rows := []ui.View{ui.Strong(title)}
	if req.Reason != "" {
		rows = append(rows, ui.Caption("Asking because "+req.Reason).Style(ui.Spec{}.TextColor(ui.TokenWarning)))
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
				ui.Checkbox(fmt.Sprintf("hunk-%d-%d", fi, hi), fmt.Sprintf("Lines %d–%d", h.B0+1, max(h.B1, h.B0+1))).
					Check(f.accepted[hi]).OnToggle(func(v bool) { p.files[fi].accepted[hi] = v }),
			))
			hunks = append(hunks, hunkView(c, f, h))
		}
	}
	if len(hunks) > 0 {
		rows = append(rows, ui.Scroll("approval-hunks", ui.Column(hunks...).Gap(th.Spacing.XS)).Height(280))
	}
	rows = append(rows, ui.TextField("approval-note", p.note).Placeholder("Note for the agent (optional)").
		OnChange(func(v string) { p.note = v }))
	actions := []ui.View{
		ui.Button("approval-deny", ui.Text("Deny")).OnClick(func() { a.answerApproval(false, false) }),
		ui.Spacer(),
	}
	if req.CanGrant && len(p.files) == 0 {
		actions = append(actions, ui.Button("approval-session", ui.Text("Allow for session")).
			Tooltip("Allow calls like this until the session ends").OnClick(func() { a.answerApproval(true, true) }))
	}
	allow := "Allow once"
	if len(p.files) > 0 {
		allow = "Apply selected"
	}
	actions = append(actions, ui.Button("approval-allow", ui.Text(allow)).Primary().OnClick(func() { a.answerApproval(true, false) }))
	rows = append(rows, ui.Row(actions...).Gap(th.Spacing.XS))
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

func (a *App) questionCard(c *ui.Ctx, q *pendingQuestion) ui.View {
	th := c.Theme()
	send := func() {
		if strings.TrimSpace(q.answer) == "" {
			return
		}
		q.reply <- q.answer
		a.agent.question = nil
	}
	return ui.Column(
		ui.Strong("The agent asks"),
		ui.Paragraph(q.text),
		ui.Row(
			ui.TextField("question-answer", q.answer).Placeholder("Your answer").DefaultFocus().
				OnChange(func(v string) { q.answer = v }).OnSubmit(func(string) { send() }).Grow(1),
			ui.Button("question-send", ui.Text("Answer")).Primary().OnClick(send),
		).Gap(th.Spacing.XS),
	).Gap(th.Spacing.S).Padding(th.Spacing.M).BorderTop(ui.TokenAccent, th.Stroke.Thick).Background(ui.TokenChromeMuted)
}

// agentInput is the composer's text field.
func agentInput(c *ui.Ctx) *ui.TextInput {
	return c.Widget("agent-input", func() any { return ui.NewTextInput(ui.TextFieldConfig{Multiline: true}) }).(*ui.TextInput)
}

// composer is the message box with the mode and model pickers under it. A
// fresh session shows it tall; once the chat starts it shrinks to grow with
// its text.
func (a *App) composer(c *ui.Ctx, fresh bool) ui.View {
	th := c.Theme()
	st := &a.agent
	minRows, maxRows := 1, 8
	if fresh {
		minRows, maxRows = 5, 14
	}
	// The box shows focus, not the field inside it.
	tf := agentInput(c)
	border := ui.TokenBorder
	if tf.Focused() {
		border = ui.TokenFocusRing
		// The panel may open over a few frames before the field is laid
		// out, so keep asking for focus until it lands.
		st.focusInput = false
	}
	field := ui.TextArea("agent-input", st.input).Rows(minRows, maxRows).
		Placeholder("Plan, build, / for commands, @ for context").
		OnChange(func(v string) { st.input = v }).
		OnSubmit(func(string) { a.sendAgent() }).
		Suggest(a.agentSuggest).
		DefaultFocus().FocusNow(st.focusInput).
		Style(ui.Background(ui.TokenUnset).Border(ui.TokenUnset, 0).When(ui.Focused, ui.Spec{}.Border(ui.TokenUnset, 0)))
	var btn ui.View
	if st.running {
		btn = ui.IconButton("agent-stop", icons.Square).Tooltip("Stop").OnClick(a.stopAgent)
	} else {
		btn = ui.Button("agent-send", ui.Icon(icons.ArrowUp, th.Metrics.IconSizeSM, th.AccentForeground)).Primary().
			Tooltip("Send (Enter)").Disabled(strings.TrimSpace(st.input) == "").OnClick(a.sendAgent)
	}
	return ui.Column(
		field,
		ui.Row(a.modePicker(), a.composerModel(), ui.Spacer(), btn).Gap(th.Spacing.XS).PaddingXY(th.Spacing.XS, 0),
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

func (a *App) modePicker() ui.View {
	st := &a.agent
	modes := agent.ModeNames(a.modes())
	items := make([]ui.MenuItem, len(modes))
	for i, m := range modes {
		m := m
		items[i] = ui.MenuItem{Label: m, Checked: m == st.mode, OnSelect: func() { st.mode = m }}
	}
	icon, ok := modeIcons[st.mode]
	if !ok {
		icon = icons.Bot
	}
	return ui.MenuButton("agent-mode", st.mode, items).IconStart(icon).Subtle().Disabled(st.running)
}

// composerModel switches between the configured providers, each shown by the
// model it runs; the model itself is picked in Settings.
func (a *App) composerModel() ui.View {
	st := &a.agent
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
		items = append(items, ui.MenuItem{Label: label, Shortcut: hint, Checked: p == st.provider,
			OnSelect: func() { st.provider = p }})
	}
	items = append(items, ui.MenuSeparator, ui.MenuItem{Label: "Models and keys…", OnSelect: a.openSettings})
	label := a.modelOf(st.provider)
	switch {
	case !a.providerReady(st.provider):
		label = "Set up a model"
	case label == "":
		label = providerLabel(st.provider)
	}
	return ui.MenuButton("agent-model", label, items).Ghost().Disabled(st.running)
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
		for _, cmd := range []string{"/mode ", "/provider ", "/new"} {
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
