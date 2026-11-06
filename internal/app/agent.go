package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/host"
	"github.com/mirzakhany/hm/internal/agent/mcp"
	"github.com/mirzakhany/hm/internal/agent/prompts"
	"github.com/mirzakhany/hm/internal/agent/tools"
	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/diff"
	"github.com/mirzakhany/hm/internal/secrets"
	"github.com/mirzakhany/hm/internal/workspace"
)

// agentState is the agent's state shared by its tabs: the stores, keys,
// models and MCP servers, and which tabs are open.
type agentState struct {
	// materialize tracks writing the built-in skills to disk.
	materialize sync.WaitGroup
	store       *agent.Store
	projects    *agent.Projects
	mcp         *mcp.Manager
	borrowed    map[string]bool // buffers opened for the agent's language tools

	tabs   []*agentTab
	active int

	// bus carries messages between agents; live holds every run that is
	// open or working, sub-agents included, by session ID.
	bus  *agent.Bus
	live map[string]*agentRun

	// sessions is the sidebar's list of saved sessions, read again when
	// one is saved, renamed or removed.
	sessions      []agent.Summary
	sessionsStale bool
	sideQuery     string
	// sideFilter shows the sidebar's session filter field; sideFocus moves
	// focus to it on the next build.
	sideFilter, sideFocus bool
	// drafts are the project tabs' unsaved forms, by tab key.
	drafts map[string]*agent.Project
	// settingsCat is the category the agent's settings tab shows.
	settingsCat string

	keys secrets.Store
	// share stands in for the browser and GitHub in tests of /export
	// and /share.
	share shareHooks
	// keyOK says which providers' keys resolve. Reading the keychain runs
	// a process, so it is read off the UI goroutine by refreshProviderKeys.
	keyOK  map[string]bool
	keySeq uint64

	// models caches each provider's model list by modelsKey; modelsErr
	// holds failed fetches and modelsLoading those in flight.
	models        map[string][]string
	modelsErr     map[string]string
	modelsLoading map[string]bool

	// prompts caches the prompt templates, read at promptsAt; a zero
	// promptsAt reads them again. shadowWarned holds the built-in names
	// already warned about.
	prompts      []prompts.Template
	promptsAt    time.Time
	shadowWarned map[string]bool
}

// agentRun is one session open in a tab: its conversation and the turn in
// progress. Several runs may work at once.
type agentRun struct {
	session  *agent.Session
	grants   *agent.Grants // "allow for this session" answers of session
	mode     string
	provider string
	input    string

	running bool
	cancel  context.CancelFunc
	// stream is the assistant text and reasoning arriving for the turn in
	// progress; it is cleared when the message joins the session.
	stream, thinking string
	streamTool       string
	retry            string // a failed model call waiting to be retried
	compacting       bool   // older messages are being summarised
	// manualCompact says the work running is /compact, not a turn.
	manualCompact bool
	calls         map[string]callView // by tool call id
	errText       string
	// info is a notice for the user, such as /context's answer.
	info string

	// ctx is the instruction files, read once for the session so its
	// system prompt stays the same and caches; nil until first needed.
	ctx       *agent.Context
	ctxWarned bool

	// from names the prompt template each message sent from one was
	// expanded from, by its text, for the chat's "from /name" label.
	from map[string]string

	approval *pendingApproval
	question *pendingQuestion

	shells *tools.Shells
	todos  *tools.Todos

	turnCheckpoint checkpoint
	lastCheckpoint *checkpoint

	// queueMu guards queue: messages typed while the turn runs, taken by
	// the agent's goroutine between steps.
	queueMu sync.Mutex
	queue   []queuedMsg

	// replay, when on, shows the transcript up to replayStep messages.
	replay     bool
	replayStep int

	// treeQuery and treeFilter narrow the /tree view.
	treeQuery  string
	treeFilter int

	// focusInput moves focus to the message box on the next build.
	focusInput bool

	// A sub-agent's run knows its parent and how deep it is nested; write
	// says it may edit. children are the sub-agents a run started, by the
	// task call that started them.
	parent   *agentRun
	depth    int
	write    bool
	children map[string]*agentRun
	// subSem bounds the sub-agents of this run working at once.
	subSem chan struct{}
}

// callView is what the transcript shows for a tool call.
type callView struct {
	summary string
	result  *tools.Result
	open    bool
}

// pendingApproval is a call waiting for the user, with the hunks of each
// file edit and which are kept.
type pendingApproval struct {
	req   agent.Approval
	files []reviewFile
	note  string
	reply chan agent.Answer
}

type reviewFile struct {
	edit     tools.FileEdit
	a, b     []string
	hunks    []diff.Hunk
	accepted []bool
	trailNL  bool
}

type pendingQuestion struct {
	text   string
	answer string
	reply  chan string
}

// initAgent prepares the agent for the open folder: sessions of the last
// folder close, and this folder's old sessions move to the shared store.
func (a *App) initAgent() {
	st := &a.agent
	if st.keys == nil {
		st.keys = secrets.OS{}
	}
	keys := st.keys
	config.Keychain = func(name string) (string, error) { return keys.Get(name) }
	for _, t := range st.tabs {
		if t.run != nil {
			a.stopRun(t.run)
			t.run.shells.StopAll()
		}
	}
	for _, r := range st.live {
		a.stopRun(r)
	}
	st.tabs, st.active = nil, 0
	st.live = map[string]*agentRun{}
	st.bus = agent.NewBus(func(id string) { a.onUI(func() { a.mailArrived(id) }) })
	st.store = agent.NewStore(config.StateDir())
	if err := st.store.Migrate(a.ws.Root); err != nil {
		a.notifs.Add("Could not move the old agent sessions: "+err.Error(), ui.ToastWarning)
	}
	if st.projects == nil {
		ps, err := agent.LoadProjects(config.StateDir())
		if err != nil {
			a.notifs.Add("Could not read the agent projects: "+err.Error(), ui.ToastWarning)
		}
		st.projects = ps
	}
	st.sessionsStale = true
	a.newMCP()
	a.startMCP(a.cfg.Current().Tools.MCP)
	a.agent.materialize.Go(func() {
		if err := host.MaterializeSkills(); err != nil {
			a.onUI(func() { a.notifs.Add("Could not write the built-in skills: "+err.Error(), ui.ToastWarning) })
		}
	})
	a.refreshProviderKeys()
}

// newRun makes a run for s, or for a new session when s is nil.
func (a *App) newRun(s *agent.Session) *agentRun {
	cfg := a.cfg.Current().Agent
	r := &agentRun{mode: cfg.DefaultMode, provider: cfg.DefaultProvider,
		shells: tools.NewShells(), todos: &tools.Todos{}, calls: map[string]callView{}, focusInput: true,
		subSem: make(chan struct{}, max(1, cfg.Subagents.MaxConcurrent))}
	if s == nil {
		s = agent.NewSession(r.mode, r.provider)
		s.Root = a.ws.Root
		s.ProjectID = a.lastProject()
		return r.with(s)
	}
	if s.Mode != "" {
		r.mode = s.Mode
	}
	if s.Provider != "" {
		r.provider = s.Provider
	}
	r.todos.Set(s.Todos)
	return r.with(s)
}

func (r *agentRun) with(s *agent.Session) *agentRun { r.session = s; return r }

// track makes r reachable by messages and by ID while it is open or
// working.
func (a *App) track(r *agentRun) {
	st := &a.agent
	if st.live == nil {
		st.live = map[string]*agentRun{}
	}
	st.live[r.session.ID] = r
	parent := ""
	if r.parent != nil {
		parent = r.parent.session.ID
	}
	name := r.session.Title
	if name == "" {
		name = "session " + r.session.ID
	}
	if st.bus != nil {
		st.bus.Register(r.session.ID, name, parent)
	}
}

// untrack forgets r once it is neither in a tab nor working.
func (a *App) untrack(r *agentRun) {
	if r.running || a.agentTabIndex(agentTabSession, r.session.ID) >= 0 {
		return
	}
	for _, ch := range r.children {
		if ch.running {
			return // its report still has to reach r
		}
	}
	delete(a.agent.live, r.session.ID)
	if r.parent != nil {
		r.shells.StopAll()
	}
	if a.agent.bus != nil {
		a.agent.bus.Unregister(r.session.ID)
	}
}

// mailArrived starts a turn for an idle session that got a message, such
// as a background sub-agent's report.
func (a *App) mailArrived(id string) {
	r := a.agent.live[id]
	if r == nil || r.running || !a.agent.bus.Pending(id) {
		return
	}
	if r.parent != nil {
		return // a finished sub-agent is not woken
	}
	if err := a.startTurn(r, ""); err != nil {
		r.errText = err.Error()
	}
}

// lastProject is the project of the newest session, for a new one.
func (a *App) lastProject() string {
	for _, s := range a.sessionList() {
		if s.ProjectID != "" {
			if _, ok := a.agent.projects.Get(s.ProjectID); ok {
				return s.ProjectID
			}
		}
		break
	}
	return ""
}

// run is the session of the active agent tab; nil when the tab is not a
// session.
func (a *App) run() *agentRun {
	if t := a.activeAgentTab(); t != nil {
		return t.run
	}
	return nil
}

// runs lists the open sessions.
func (a *App) runs() []*agentRun {
	var out []*agentRun
	for _, t := range a.agent.tabs {
		if t.run != nil {
			out = append(out, t.run)
		}
	}
	return out
}

// anyRunning reports whether a session is working.
func (a *App) anyRunning() bool {
	for _, r := range a.runs() {
		if r.running {
			return true
		}
	}
	return false
}

// modes resolves the configured modes for this workspace.
func (a *App) modes() map[string]agent.Mode { return a.agentHost().Modes() }

// queuedMsg is a message waiting for the running agent. A follow-up waits
// for the turn to end; otherwise it steers, skipping the tool calls not yet
// started.
type queuedMsg struct {
	Text     string
	FollowUp bool
}

// sendAgent sends the active session's message box.
func (a *App) sendAgent() {
	if r := a.run(); r != nil {
		a.sendRun(r)
	}
}

// sendRun starts a turn with r's message box, or runs a slash command.
// While a turn runs the message steers the agent after its current tool
// call.
func (a *App) sendRun(r *agentRun) { a.sendRunAs(r, false) }

// sendRunAs is sendRun; while a turn runs, a follow-up waits for the turn
// to end instead of steering.
func (a *App) sendRunAs(r *agentRun, followUp bool) {
	text := strings.TrimSpace(r.input)
	if text == "" {
		return
	}
	r.info = ""
	if strings.HasPrefix(text, "/") {
		r.input = ""
		a.slashCommand(r, text)
		return
	}
	a.sendText(r, a.expandContext(text), followUp)
}

// sendText starts a turn with text, or queues it while a turn runs.
func (a *App) sendText(r *agentRun, text string, followUp bool) {
	if r.running {
		r.input = ""
		r.queueMu.Lock()
		r.queue = append(r.queue, queuedMsg{Text: text, FollowUp: followUp})
		r.queueMu.Unlock()
		return
	}
	if err := a.startTurn(r, text); err != nil {
		r.errText = err.Error()
		return
	}
	r.input = ""
}

// agentFor builds the agent for one turn of r: the provider, guard, tools,
// policy and system prompt as they are now.
func (a *App) agentFor(r *agentRun) (*agent.Agent, error) {
	s := r.session
	if r.grants == nil {
		r.grants = agent.NewGrants() // grants last the session, not one message
	}
	store := a.agent.store
	// A sub-agent asks the user through the session at the top, the one
	// on screen.
	asker, label := r, ""
	if r.parent != nil {
		for asker.parent != nil {
			asker = asker.parent
		}
		label = "sub-agent \"" + s.Title + "\""
	}
	ctx := a.agentContext(r)
	ag, err := a.agentHost().Build(host.Turn{
		Session: s, Mode: r.mode, Provider: r.provider, Grants: r.grants,
		Env: agentEnv{a}, Context: ctx, Tools: a.agentTools(r),
		Hooks: agent.Hooks{
			Event: func(e agent.Event) { a.onUI(func() { a.agentEvent(r, e) }) },
			Approve: func(ctx context.Context, req agent.Approval) agent.Answer {
				if label != "" {
					req.Reason = strings.TrimSpace(label + " asks; " + req.Reason)
				}
				return a.agentApprove(ctx, asker, req)
			},
			Blocked: func(tool, why string) {
				a.onUI(func() {
					a.toast("Guard blocked "+tool+": "+why, ui.ToastWarning,
						notifAction{label: "Edit guard.yaml", run: func() { a.openGuard(false) }})
				})
			},
			Ask: func(ctx context.Context, q string) (string, error) {
				if label != "" {
					q = label + " asks: " + q
				}
				return a.agentAsk(ctx, asker, q)
			},
			Apply: func(_ context.Context, edits []tools.FileEdit) error {
				var err error
				if cerr := a.callUI(func() { err = a.applyAgentEdits(r, edits) }); cerr != nil {
					return cerr
				}
				return err
			},
			Steer:    func() []string { return a.inboxOf(r, false) },
			FollowUp: func() []string { return a.inboxOf(r, true) },
			Persist:  func() { _ = store.Save(s) },
		},
	})
	if err != nil {
		return nil, err
	}
	if !r.ctxWarned {
		r.ctxWarned = true
		for _, w := range ctx.Warnings {
			a.notifs.Add("Agent instructions: "+w, ui.ToastWarning)
		}
	}
	return ag, nil
}

// startTurn runs the agent on prompt in the background.
func (a *App) startTurn(r *agentRun, prompt string) error {
	ag, err := a.agentFor(r)
	if err != nil {
		return err
	}
	if prompt == "" {
		// Woken by mail: the mail is the message.
		prompt = strings.Join(a.allQueued(r), "\n\n")
		if prompt == "" {
			return nil
		}
	}
	a.launch(r, func(ctx context.Context) error { return ag.Run(ctx, r.session, prompt) })
	return nil
}

// compactNow runs /compact: it summarises r's older messages in the
// background, keeping what focus names.
func (a *App) compactNow(r *agentRun, focus string) {
	if r.running {
		r.errText = "Wait for the turn to finish, then /compact"
		return
	}
	ag, err := a.agentFor(r)
	if err != nil {
		r.errText = err.Error()
		return
	}
	r.manualCompact = true
	a.launch(r, func(ctx context.Context) error {
		err := ag.Compact(ctx, r.session, focus)
		if errors.Is(err, agent.ErrNothingToCompact) {
			a.onUI(func() { r.info = "Nothing to compact yet" })
			return nil
		}
		if err != nil {
			return fmt.Errorf("Could not compact the conversation: %w", err)
		}
		return nil
	})
}

// launch runs work, a turn of r, in the background.
func (a *App) launch(r *agentRun, work func(ctx context.Context) error) {
	s := r.session
	ctx, cancel := context.WithCancel(context.Background())
	r.running, r.cancel, r.errText = true, cancel, ""
	r.stream, r.thinking, r.streamTool, r.retry, r.compacting = "", "", "", "", false
	r.turnCheckpoint = checkpoint{}
	s.SetStatus(agent.StatusRunning)
	a.track(r)
	a.agent.bus.SetRunning(s.ID, true)
	go func() {
		err := work(ctx)
		a.onUI(func() { a.turnEnded(r, err) })
	}()
}

// turnEnded settles r after its turn, and starts the next one when
// messages are still waiting.
func (a *App) turnEnded(r *agentRun, err error) {
	s := r.session
	r.running, r.cancel = false, nil
	r.stream, r.thinking, r.streamTool, r.retry, r.compacting = "", "", "", "", false
	r.approval, r.question = nil, nil
	if !a.anyRunning() {
		a.releaseAgentBuffers()
	}
	if len(r.turnCheckpoint.order) > 0 {
		cp := r.turnCheckpoint
		r.lastCheckpoint = &cp
	}
	r.todosSnapshot()
	if err != nil && !errors.Is(err, context.Canceled) && r.errText == "" {
		r.errText = err.Error()
	}
	s.SetStatus(agent.StatusIdle)
	a.agent.bus.SetRunning(s.ID, false)
	if serr := a.agent.store.Save(s); serr != nil {
		a.notifs.Add("Could not save the agent session: "+serr.Error(), ui.ToastWarning)
	}
	a.agent.sessionsStale = true
	if r.parent != nil {
		return // the parent reports the sub-agent's work
	}
	manual := r.manualCompact
	r.manualCompact = false
	if err == nil && !manual {
		a.notifs.Add("Agent finished: "+s.Title, ui.ToastSuccess,
			notifAction{label: "Open Agent", run: func() { a.focusRun(r) }})
	}
	if q := a.allQueued(r); len(q) > 0 && !errors.Is(err, context.Canceled) {
		if serr := a.startTurn(r, strings.Join(q, "\n\n")); serr != nil {
			r.errText = serr.Error()
		}
	}
}

// inboxOf is what reaches r's agent between steps, the follow-ups or the
// steering messages: the user's queued messages, then other agents' mail.
func (a *App) inboxOf(r *agentRun, followUp bool) []string {
	out := r.takeQueue(followUp)
	if a.agent.bus != nil {
		for _, m := range a.agent.bus.Take(r.session.ID, followUp) {
			out = append(out, host.MailText(m))
		}
	}
	return out
}

// allQueued takes every waiting message, steering then follow-ups, for a
// new turn.
func (a *App) allQueued(r *agentRun) []string {
	return append(a.inboxOf(r, false), a.inboxOf(r, true)...)
}

// takeQueue returns and removes the follow-ups, or the steering messages,
// waiting for the agent.
func (r *agentRun) takeQueue(followUp bool) []string {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	var out []string
	kept := r.queue[:0]
	for _, m := range r.queue {
		if m.FollowUp == followUp {
			out = append(out, m.Text)
		} else {
			kept = append(kept, m)
		}
	}
	r.queue = kept
	return out
}

// queued copies the waiting messages for display.
func (r *agentRun) queued() []queuedMsg {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	return append([]queuedMsg(nil), r.queue...)
}

// editQueued takes waiting message i back into the message box, after
// what is already there, to be changed and sent again.
func (r *agentRun) editQueued(i int) {
	r.queueMu.Lock()
	if i < 0 || i >= len(r.queue) {
		r.queueMu.Unlock()
		return
	}
	text := r.queue[i].Text
	r.queue = append(r.queue[:i], r.queue[i+1:]...)
	r.queueMu.Unlock()
	if strings.TrimSpace(r.input) != "" {
		text = strings.TrimRight(r.input, "\n") + "\n\n" + text
	}
	r.input, r.focusInput = text, true
}

// unqueue drops waiting message i.
func (r *agentRun) unqueue(i int) {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	if i >= 0 && i < len(r.queue) {
		r.queue = append(r.queue[:i], r.queue[i+1:]...)
	}
}

func (r *agentRun) todosSnapshot() {
	if r.session != nil && r.todos != nil {
		r.session.SetTodos(r.todos.Items())
	}
}

// stopAgent cancels the active session's turn.
func (a *App) stopAgent() {
	if r := a.run(); r != nil {
		a.stopRun(r)
	}
}

// stopAllAgents cancels every running turn.
func (a *App) stopAllAgents() {
	for _, r := range a.runs() {
		a.stopRun(r)
	}
}

// stopRun cancels r's turn in progress; waiting messages are dropped.
func (a *App) stopRun(r *agentRun) {
	r.queueMu.Lock()
	r.queue = nil
	r.queueMu.Unlock()
	for _, ch := range r.children {
		a.stopRun(ch)
	}
	if r.cancel != nil {
		r.cancel()
	}
	if p := r.approval; p != nil {
		p.reply <- agent.Answer{Allow: false, Note: "stopped by the user"}
		r.approval = nil
	}
	if q := r.question; q != nil {
		q.reply <- ""
		r.question = nil
	}
}

// agentEvent applies a report from r's agent goroutine.
func (a *App) agentEvent(r *agentRun, e agent.Event) {
	switch e.Kind {
	case agent.EvText:
		r.stream += e.Text
		r.retry = ""
	case agent.EvThinking:
		r.thinking += e.Text
		r.retry = ""
	case agent.EvToolStart:
		r.streamTool, r.retry = e.Tool, ""
	case agent.EvRetry:
		r.retry = e.Text
	case agent.EvCompacting:
		r.compacting, r.retry = true, ""
	case agent.EvCompacted:
		r.compacting = false
	case agent.EvMessage:
		r.stream, r.thinking, r.streamTool, r.retry = "", "", "", ""
		r.todosSnapshot()
	case agent.EvToolCall:
		r.calls[e.CallID] = callView{summary: e.Summary}
	case agent.EvToolResult:
		cv := r.calls[e.CallID]
		res := e.Result
		cv.result = &res
		r.calls[e.CallID] = cv
	case agent.EvError:
		r.errText = e.Text
	}
}

// agentApprove shows an approval card in r and waits for the user.
func (a *App) agentApprove(ctx context.Context, r *agentRun, req agent.Approval) agent.Answer {
	reply := make(chan agent.Answer, 1)
	_ = a.callUI(func() {
		p := &pendingApproval{req: req, reply: reply}
		for _, e := range req.Plan.Edits {
			rf := reviewFile{edit: e, a: diff.Lines(string(e.Old)), b: diff.Lines(string(e.New))}
			rf.trailNL = len(e.New) == 0 || strings.HasSuffix(string(e.New), "\n")
			rf.hunks = diff.Hunks(rf.a, rf.b, 3)
			rf.accepted = make([]bool, len(rf.hunks))
			for i := range rf.accepted {
				rf.accepted[i] = true
			}
			p.files = append(p.files, rf)
		}
		r.approval = p
		a.agentOpen = true
	})
	select {
	case ans := <-reply:
		return ans
	case <-ctx.Done():
		return agent.Answer{Allow: false, Note: "cancelled"}
	}
}

// answerApproval replies to r's card.
func (a *App) answerApproval(r *agentRun, allow, forSession bool) {
	p := r.approval
	if p == nil {
		return
	}
	r.approval = nil
	ans := agent.Answer{Allow: allow, ForSession: forSession, Note: strings.TrimSpace(p.note)}
	if allow && len(p.files) > 0 {
		for _, f := range p.files {
			partial := false
			for _, ok := range f.accepted {
				partial = partial || !ok
			}
			e := f.edit
			if partial && e.New != nil && e.Old != nil {
				e.New = []byte(diff.Apply(f.a, f.b, f.hunks, f.accepted, f.trailNL))
				if string(e.New) == string(e.Old) {
					continue // every hunk turned down
				}
			}
			ans.Edits = append(ans.Edits, e)
		}
		if ans.Edits == nil {
			ans.Edits = []tools.FileEdit{}
		}
	}
	p.reply <- ans
}

// agentAsk puts ask_user's question in r and waits for the answer.
func (a *App) agentAsk(ctx context.Context, r *agentRun, question string) (string, error) {
	reply := make(chan string, 1)
	_ = a.callUI(func() {
		r.question = &pendingQuestion{text: question, reply: reply}
		a.agentOpen = true
	})
	select {
	case ans := <-reply:
		if ans == "" {
			return "", errors.New("the user did not answer")
		}
		return ans, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// slashCommand runs /mode, /provider, /context, /compact, /tree, /fork,
// /label, /export, /share and /new, or else the prompt template of that
// name.
func (a *App) slashCommand(r *agentRun, text string) {
	f := strings.Fields(text)
	switch f[0] {
	case "/mode":
		modes := a.modes()
		if len(f) < 2 {
			r.errText = "Modes: " + strings.Join(agent.ModeNames(modes), ", ")
			return
		}
		if _, ok := modes[f[1]]; !ok {
			r.errText = "Unknown mode " + f[1] + ". Modes: " + strings.Join(agent.ModeNames(modes), ", ")
			return
		}
		r.mode, r.errText = f[1], ""
		a.toast("Agent mode: "+f[1], ui.ToastInfo)
	case "/provider":
		if len(f) < 2 {
			r.errText = "Providers: " + strings.Join(a.providerNames(), ", ")
			return
		}
		if _, ok := a.cfg.Current().Agent.Providers[f[1]]; !ok {
			r.errText = "Unknown provider " + f[1]
			return
		}
		r.provider, r.errText = f[1], ""
	case "/new", "/clear":
		a.newAgentSession()
	case "/context":
		r.errText, r.info = "", contextInfo(a.agentContext(r))
	case "/compact":
		a.compactNow(r, strings.TrimSpace(text[len(f[0]):]))
	case "/tree":
		a.openTree(r)
	case "/fork":
		id := ""
		if len(f) > 1 {
			var err error
			if id, err = findEntry(r.session, f[1]); err != nil {
				r.errText = err.Error()
				return
			}
		}
		a.forkAt(r, id)
	case "/label":
		a.labelLast(r, strings.TrimSpace(text[len(f[0]):]))
	case "/export":
		a.exportCommand(r, strings.TrimSpace(text[len(f[0]):]))
	case "/share":
		a.shareCommand(r, f[1:])
	default:
		if a.runTemplate(r, f[0], strings.TrimSpace(text[len(f[0]):])) {
			return
		}
		r.errText = "Unknown command " + f[0] + ". Try /mode, /provider, /context, /compact, /tree, /fork, /label, /export, /share or /new, or add a prompt template."
	}
}

// agentContext is r's instruction files, read the first time they are
// needed. A sub-agent shares its parent's.
func (a *App) agentContext(r *agentRun) *agent.Context {
	if r.parent != nil {
		return a.agentContext(r.parent)
	}
	if r.ctx != nil {
		return r.ctx
	}
	r.ctx = a.agentHost().Context()
	return r.ctx
}

// contextChanged drops the instruction files of sessions with nothing in
// them yet, so they read the files again; a session under way keeps its
// own. A new file shows only as its folder changing, and reading the
// files again is cheap, so any folder change counts.
func (a *App) contextChanged(ch workspace.Changes) {
	names := map[string]bool{"SYSTEM.md": true}
	for _, n := range a.cfg.Current().Agent.Context.Files {
		names[n] = true
	}
	hit := len(ch.Dirs) > 0
	for p := range ch.Files {
		if names[filepath.Base(p)] {
			hit = true
			break
		}
	}
	if !hit {
		return
	}
	for _, t := range a.agent.tabs {
		if r := t.run; r != nil && r.parent == nil && !r.running && len(r.session.Snapshot()) == 0 {
			r.ctx, r.ctxWarned = nil, false
		}
	}
}

// contextInfo is /context's answer.
func contextInfo(c *agent.Context) string {
	files := c.Loaded()
	if len(files) == 0 && len(c.Warnings) == 0 {
		return "No instruction files loaded. The agent reads AGENTS.md or CLAUDE.md (agent.context.files) from ~/.config/hm and from each folder down to the workspace, and SYSTEM.md from .hm or ~/.config/hm."
	}
	var b strings.Builder
	b.WriteString("Instruction files in this session's system prompt:")
	for _, f := range files {
		b.WriteString("\n• " + f.Rel)
		if f.Path == pathOf(c.System) {
			if c.Append {
				b.WriteString(" (added to the base prompt)")
			} else {
				b.WriteString(" (replaces the base prompt)")
			}
		}
	}
	for _, w := range c.Warnings {
		b.WriteString("\n⚠ " + w)
	}
	return b.String()
}

func pathOf(f *agent.ContextFile) string {
	if f == nil {
		return ""
	}
	return f.Path
}

// newAgentSession opens a new session in a tab.
func (a *App) newAgentSession() {
	a.openRunTab(a.newRun(nil))
	a.agentOpen = true
}

// resumeAgentSession opens a saved session in a tab, or focuses its tab.
// A session of another folder asks to open that folder first.
func (a *App) resumeAgentSession(id string) {
	if i := a.agentTabIndex(agentTabSession, id); i >= 0 {
		a.focusAgentTab(i)
		return
	}
	if r := a.agent.live[id]; r != nil {
		a.openRunTab(r) // a sub-agent at work
		return
	}
	s, err := a.agent.store.Load(id)
	if err != nil {
		a.fail("Could not open the session", err)
		return
	}
	if s.Root != "" && s.Root != a.ws.Root {
		root := s.Root
		a.ctx.Dialogs().ShowAction("Open "+filepath.Base(root)+"?",
			"This session works in "+root+". Open that folder to continue it?",
			func() {
				a.confirmDiscard("Open folder?", func() {
					a.openFolder(root)
					a.agentOpen = true
					a.resumeAgentSession(id)
				})
			}, nil)
		return
	}
	// Saved mid-turn by a run of hm that is gone.
	host.Recover(a.agent.store, s)
	a.openRunTab(a.newRun(s))
}

func (a *App) providerNames() []string {
	var out []string
	for n := range a.cfg.Current().Agent.Providers {
		out = append(out, n)
	}
	sortStrings(out)
	return out
}

// expandContext turns @file, @folder, @selection, @diagnostics and
// @git-diff mentions into context attached after the message.
func (a *App) expandContext(text string) string {
	var extra []string
	for _, w := range strings.Fields(text) {
		if !strings.HasPrefix(w, "@") || len(w) < 2 {
			continue
		}
		name := strings.TrimRight(w[1:], ".,;:!?")
		switch name {
		case "selection":
			if ed := a.ws.ActiveEditor(); ed != nil && ed.HasSelection() {
				line, _ := ed.LineCol(func() int { lo, _ := ed.Selection(); return lo }())
				extra = append(extra, fmt.Sprintf("<selection file=%q line=%d>\n%s\n</selection>", a.ws.Rel(ed.Path), line+1, ed.SelectedText()))
			}
		case "diagnostics":
			if ds := (agentEnv{a}).diagnosticsNow(); len(ds) > 0 {
				extra = append(extra, "<diagnostics>\n"+strings.Join(ds, "\n")+"\n</diagnostics>")
			}
		case "git-diff":
			extra = append(extra, "Run git_diff to see the uncommitted changes the user means.")
		default:
			path := filepath.Join(a.ws.Root, filepath.FromSlash(name))
			st, err := os.Stat(path)
			switch {
			case err != nil:
			case st.IsDir():
				entries, _ := os.ReadDir(path)
				var names []string
				for _, e := range entries {
					n := e.Name()
					if e.IsDir() {
						n += "/"
					}
					names = append(names, n)
				}
				extra = append(extra, fmt.Sprintf("<folder path=%q>\n%s\n</folder>", name, strings.Join(names, "\n")))
			case st.Size() < 200<<10:
				var data []byte
				if ed := a.bufs.Get(path); ed != nil {
					data = ed.Bytes()
				} else {
					data, _ = os.ReadFile(path)
				}
				extra = append(extra, fmt.Sprintf("<file path=%q>\n%s\n</file>", name, data))
			}
		}
	}
	if len(extra) == 0 {
		return text
	}
	return text + "\n\n" + strings.Join(extra, "\n\n")
}

// diagnosticsNow is Diagnostics on the UI goroutine.
func (e agentEnv) diagnosticsNow() []string {
	var out []string
	for _, p := range e.a.problems() {
		out = append(out, fmt.Sprintf("%s:%d:%d: %s", e.a.ws.Rel(p.path), p.diag.Range.Start.Line+1,
			p.diag.Range.Start.Character+1, p.diag.Message))
	}
	return out
}

// askAboutSelection opens the agent with the selection attached.
func (a *App) askAboutSelection() {
	a.agentOpen = true
	r := a.run()
	if r == nil {
		r = a.newRun(nil)
		a.openRunTab(r)
	}
	r.focusInput = true
	if r.input == "" {
		r.input = "@selection "
	}
}

// saveProviderKey stores a key in the keychain and points the provider's
// api_key at it.
func (a *App) saveProviderKey(name, key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if err := a.agent.keys.Set(name, key); err != nil {
		a.fail("Could not save the key", err)
		return
	}
	ref := "keychain:" + name
	if pc := a.cfg.Current().Agent.Providers[name]; pc.APIKey != ref {
		a.setUserSetting([]string{"agent", "providers", name, "api_key"}, ref)
	}
	a.toast("Saved the "+name+" key in the keychain", ui.ToastSuccess)
	a.refreshProviderKeys()
	a.forgetModels(name)
	a.compl.forgetProvider()
}

// hasProviderKey reports whether the provider's key resolves, as last read
// by refreshProviderKeys. It never reads the keychain: Body calls it.
func (a *App) hasProviderKey(name string) bool { return a.agent.keyOK[name] }

// refreshProviderKeys resolves every provider's key in the background and
// updates what hasProviderKey reports.
func (a *App) refreshProviderKeys() {
	st := &a.agent
	st.keySeq++
	seq := st.keySeq
	refs := map[string]string{}
	for name, pc := range a.cfg.Current().Agent.Providers {
		refs[name] = pc.APIKey
	}
	go func() {
		ok := make(map[string]bool, len(refs))
		for name, ref := range refs {
			if ref != "" {
				v, err := config.Resolve(ref)
				ok[name] = err == nil && v != ""
			}
		}
		a.onUI(func() {
			if seq == st.keySeq {
				st.keyOK = ok
			}
		})
	}()
}
