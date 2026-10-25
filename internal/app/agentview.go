package app

import (
	"fmt"
	"sort"
	"strings"

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
	body := []ui.View{a.agentHeader(c)}
	if st.sessionsOpen {
		body = append(body, a.sessionList(c))
	} else {
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
		body = append(body, ui.Row(
			ui.Paragraph(st.errText).Style(ui.Spec{}.TextColor(ui.TokenError)).Grow(1),
			ui.IconButton("agent-err-close", icons.X).OnClick(func() { st.errText = "" }),
		).Gap(th.Spacing.XS).PaddingXY(th.Spacing.S, th.Spacing.XXS))
	}
	body = append(body, a.agentInput(c))
	return ui.Column(body...).Grow(1).Background(ui.TokenChrome).BorderLeft(ui.TokenBorder, th.Stroke.Thin)
}

func (a *App) agentHeader(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	modes := agent.ModeNames(a.modes())
	modeOpts := make([]ui.SelectOption, len(modes))
	modeSel := 0
	for i, m := range modes {
		modeOpts[i] = ui.SelectOption{Label: m, Value: m}
		if m == st.mode {
			modeSel = i
		}
	}
	provs := a.providerNames()
	provOpts := make([]ui.SelectOption, len(provs))
	provSel := 0
	for i, p := range provs {
		label := p
		if !a.hasProviderKey(p) && a.cfg.Current().Agent.Providers[p].Type == "openai" && !strings.Contains(a.cfg.Current().Agent.Providers[p].BaseURL, "localhost") {
			label += " (no key)"
		}
		provOpts[i] = ui.SelectOption{Label: label, Value: p}
		if p == st.provider {
			provSel = i
		}
	}
	usage := ""
	if s := st.session; s != nil && s.UsageNow().Input+s.UsageNow().Output > 0 {
		u := s.UsageNow()
		usage = fmt.Sprintf("↑%s ↓%s", tokens(u.Input+u.CacheRead+u.CacheWrite), tokens(u.Output))
		if u.Cost > 0 {
			usage += fmt.Sprintf(" · $%.3f", u.Cost)
		}
	}
	return ui.Column(
		ui.Row(
			ui.Strong("Agent"),
			ui.Spacer(),
			ui.Caption(usage).Tooltip("Tokens in (cached included) and out, and cost when known"),
			ui.IconButton("agent-undo", icons.Undo2).Tooltip("Undo the last agent turn's file changes").
				Disabled(st.lastCheckpoint == nil || st.running).OnClick(a.undoAgentTurn),
			ui.IconButton("agent-history", icons.Clock).Tooltip("Sessions").
				OnClick(func() { st.sessionsOpen = !st.sessionsOpen }),
			ui.IconButton("agent-new", icons.SquarePen).Tooltip("New session").Disabled(st.running).OnClick(a.newAgentSession),
			ui.IconButton("agent-close", icons.X).Tooltip("Close").OnClick(func() { a.agentOpen = false }),
		).Gap(th.Spacing.XXS),
		ui.Row(
			ui.Select("agent-mode", modeOpts).Selected(modeSel).Grow(1).Disabled(st.running).
				OnChange(func(v string) { st.mode = v }),
			ui.Select("agent-provider", provOpts).Selected(provSel).Grow(1).Disabled(st.running).
				OnChange(func(v string) { st.provider = v }),
		).Gap(th.Spacing.XS),
	).Gap(th.Spacing.XS).PaddingXY(th.Spacing.M, th.Spacing.S).BorderBottom(ui.TokenBorder, th.Stroke.Thin)
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

func (a *App) sessionList(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	rows := []ui.View{ui.Caption("Sessions in this workspace")}
	for i, s := range st.store.List() {
		s := s
		title := s.Title
		if title == "" {
			title = "(empty)"
		}
		rows = append(rows, ui.Button(fmt.Sprintf("agent-sess-%d", i), ui.Column(
			ui.Text(title).Ellipsis(ui.EllipsisEnd),
			ui.Caption(s.Mode+" · "+s.Updated.Format("Jan 2 15:04")),
		).Gap(th.Spacing.XXS)).Ghost().HoverFill().OnClick(func() {
			a.resumeAgentSession(s.ID)
			st.sessionsOpen = false
		}))
	}
	if len(rows) == 1 {
		rows = append(rows, ui.Muted("No saved sessions yet"))
	}
	return ui.Scroll("agent-sessions", ui.Column(rows...).Gap(th.Spacing.XS).Padding(th.Spacing.M)).Grow(1)
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
					rows = append(rows, ui.Paragraph(strings.TrimSpace(b.Text)))
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
			rows = append(rows, ui.Paragraph(st.stream))
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
	return ui.Scroll("agent-transcript", ui.Column(rows...).Gap(th.Spacing.S).Padding(th.Spacing.M)).Grow(1)
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

func (a *App) agentInput(c *ui.Ctx) ui.View {
	th := c.Theme()
	st := &a.agent
	field := ui.TextField("agent-input", st.input).
		Placeholder(fmt.Sprintf("Ask in %s mode… (/mode to switch)", st.mode)).
		OnChange(func(v string) { st.input = v }).
		OnSubmit(func(string) { a.sendAgent() }).
		Suggest(a.agentSuggest).
		Grow(1)
	var btn ui.View
	if st.running {
		btn = ui.Button("agent-stop", ui.Text("Stop")).IconStart(icons.Square).OnClick(a.stopAgent)
	} else {
		btn = ui.Button("agent-send", ui.Text("Send")).Primary().IconStart(icons.Send).
			Disabled(strings.TrimSpace(st.input) == "").OnClick(a.sendAgent)
	}
	return ui.Row(field, btn).Gap(th.Spacing.XS).Padding(th.Spacing.S).BorderTop(ui.TokenBorder, th.Stroke.Thin)
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
