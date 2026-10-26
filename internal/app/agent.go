package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/mcp"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/skills"
	"github.com/mirzakhany/hm/internal/agent/tools"
	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/diff"
	"github.com/mirzakhany/hm/internal/secrets"
	"github.com/mirzakhany/hm/internal/workspace"
)

// agentState is the agent panel's state.
type agentState struct {
	// materialize tracks writing the built-in skills to disk.
	materialize sync.WaitGroup
	store       *agent.Store
	session     *agent.Session
	mode        string
	provider    string
	input       string

	running bool
	cancel  context.CancelFunc
	// stream is the assistant text and reasoning arriving for the turn in
	// progress; it is cleared when the message joins the session.
	stream, thinking string
	streamTool       string
	calls            map[string]callView // by tool call id
	errText          string

	approval *pendingApproval
	question *pendingQuestion

	shells   *tools.Shells
	todos    *tools.Todos
	mcp      *mcp.Manager
	borrowed map[string]bool // buffers opened for the agent's language tools

	turnCheckpoint checkpoint
	lastCheckpoint *checkpoint

	sessionsOpen bool
	// sessions is the history list, read when it opens.
	sessions []agent.Summary
	// focusInput moves focus to the message box on the next build.
	focusInput bool
	keys       secrets.Store
	// keyOK says which providers' keys resolve. Reading the keychain runs
	// a process, so it is read off the UI goroutine by refreshProviderKeys.
	keyOK  map[string]bool
	keySeq uint64

	// models caches each provider's model list by modelsKey; modelsErr
	// holds failed fetches and modelsLoading those in flight.
	models        map[string][]string
	modelsErr     map[string]string
	modelsLoading map[string]bool
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

// initAgent prepares the agent panel for the open folder.
func (a *App) initAgent() {
	st := &a.agent
	if st.keys == nil {
		st.keys = secrets.OS{}
	}
	keys := st.keys
	config.Keychain = func(name string) (string, error) { return keys.Get(name) }
	st.store = agent.NewStore(workspace.StateDir(), a.ws.Root)
	st.shells, st.todos = tools.NewShells(), &tools.Todos{}
	st.calls = map[string]callView{}
	a.newMCP()
	a.startMCP(a.cfg.Current().Tools.MCP)
	a.agent.materialize.Go(func() {
		if err := skills.Materialize(builtinSkillsDir()); err != nil {
			a.onUI(func() { a.notifs.Add("Could not write the built-in skills: "+err.Error(), ui.ToastWarning) })
		}
	})
	cfg := a.cfg.Current().Agent
	if st.mode == "" {
		st.mode = cfg.DefaultMode
	}
	if st.provider == "" {
		st.provider = cfg.DefaultProvider
	}
	st.session = nil
	a.refreshProviderKeys()
}

func (a *App) currentSession() *agent.Session {
	st := &a.agent
	if st.session == nil {
		st.session = agent.NewSession(st.mode, st.provider)
	}
	return st.session
}

// modes resolves the configured modes for this workspace.
func (a *App) modes() map[string]agent.Mode {
	return agent.Modes(a.cfg.Current().Modes, a.ws.Root)
}

// newProvider builds the selected provider, resolving its key.
func (a *App) newProvider(name string) (provider.Provider, config.Provider, error) {
	pc, ok := a.cfg.Current().Agent.Providers[name]
	if !ok {
		return nil, pc, fmt.Errorf("no provider named %q in agent.providers", name)
	}
	p, err := buildProvider(name, pc)
	return p, pc, err
}

// buildProvider resolves pc's key and builds the provider. Reading the
// keychain runs a process, so callers off the UI goroutine are welcome.
func buildProvider(name string, pc config.Provider) (provider.Provider, error) {
	key := ""
	if pc.APIKey != "" {
		var err error
		key, err = config.Resolve(pc.APIKey)
		if err != nil && pc.Type == "anthropic" && strings.HasPrefix(pc.APIKey, "keychain:") {
			// The SDK may still find credentials of its own.
			key, err = "", nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s API key: %w; add it in Settings → Agent", name, err)
		}
	}
	return provider.New(provider.Config{Type: pc.Type, BaseURL: pc.BaseURL, APIKey: key})
}

// sendAgent starts a turn with the input box's text, or runs a slash
// command.
func (a *App) sendAgent() {
	st := &a.agent
	text := strings.TrimSpace(st.input)
	if text == "" || st.running {
		return
	}
	st.input = ""
	if strings.HasPrefix(text, "/") {
		a.slashCommand(text)
		return
	}
	modes := a.modes()
	mode, ok := modes[st.mode]
	if !ok {
		st.errText = "Unknown mode " + st.mode + "; pick one with /mode"
		return
	}
	prov, pc, err := a.newProvider(st.provider)
	if err != nil {
		st.errText = err.Error()
		return
	}
	s := a.currentSession()
	s.Mode, s.Provider = st.mode, st.provider
	ts := a.agentToolset(mode)
	policy := agent.NewPolicy(a.ws.Root, ts.rules)
	policy.ReadRoots = ts.readRoots
	ag := &agent.Agent{
		Provider: prov, Model: pc.Model, Effort: pc.Effort,
		System: agent.System(mode, a.ws.Root, s.Created, ts.extra),
		Tools:  ts.reg, Policy: policy, Env: agentEnv{a},
		Hooks: agent.Hooks{
			Event:   func(e agent.Event) { a.onUI(func() { a.agentEvent(e) }) },
			Approve: a.agentApprove,
			Ask:     a.agentAsk,
			Apply: func(_ context.Context, edits []tools.FileEdit) error {
				var err error
				if cerr := a.callUI(func() { err = a.applyAgentEdits(edits) }); cerr != nil {
					return cerr
				}
				return err
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	st.running, st.cancel, st.errText = true, cancel, ""
	st.stream, st.thinking, st.streamTool = "", "", ""
	st.turnCheckpoint = checkpoint{}
	prompt := a.expandContext(text)
	go func() {
		err := ag.Run(ctx, s, prompt)
		a.onUI(func() {
			st.running, st.cancel = false, nil
			st.stream, st.thinking, st.streamTool = "", "", ""
			st.approval, st.question = nil, nil
			a.releaseAgentBuffers()
			if len(st.turnCheckpoint.order) > 0 {
				cp := st.turnCheckpoint
				st.lastCheckpoint = &cp
			}
			st.todosSnapshot()
			if err != nil && !errors.Is(err, context.Canceled) && st.errText == "" {
				st.errText = err.Error()
			}
			if serr := st.store.Save(s); serr != nil {
				a.notifs.Add("Could not save the agent session: "+serr.Error(), ui.ToastWarning)
			}
			if err == nil {
				a.notifs.Add("Agent finished: "+s.Title, ui.ToastSuccess)
			}
		})
	}()
}

func (st *agentState) todosSnapshot() {
	if st.session != nil && st.todos != nil {
		st.session.Todos = st.todos.Items()
	}
}

// stopAgent cancels the turn in progress.
func (a *App) stopAgent() {
	st := &a.agent
	if st.cancel != nil {
		st.cancel()
	}
	if p := st.approval; p != nil {
		p.reply <- agent.Answer{Allow: false, Note: "stopped by the user"}
		st.approval = nil
	}
	if q := st.question; q != nil {
		q.reply <- ""
		st.question = nil
	}
}

// agentEvent applies a report from the agent goroutine to the panel.
func (a *App) agentEvent(e agent.Event) {
	st := &a.agent
	switch e.Kind {
	case agent.EvText:
		st.stream += e.Text
	case agent.EvThinking:
		st.thinking += e.Text
	case agent.EvToolStart:
		st.streamTool = e.Tool
	case agent.EvMessage:
		st.stream, st.thinking, st.streamTool = "", "", ""
	case agent.EvToolCall:
		st.calls[e.CallID] = callView{summary: e.Summary}
	case agent.EvToolResult:
		cv := st.calls[e.CallID]
		res := e.Result
		cv.result = &res
		st.calls[e.CallID] = cv
		st.todosSnapshot()
	case agent.EvError:
		st.errText = e.Text
	}
}

// agentApprove shows an approval card and waits for the user.
func (a *App) agentApprove(ctx context.Context, req agent.Approval) agent.Answer {
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
		a.agent.approval = p
		a.agentOpen = true
	})
	select {
	case ans := <-reply:
		return ans
	case <-ctx.Done():
		return agent.Answer{Allow: false, Note: "cancelled"}
	}
}

// answerApproval replies to the card.
func (a *App) answerApproval(allow, forSession bool) {
	p := a.agent.approval
	if p == nil {
		return
	}
	a.agent.approval = nil
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

// agentAsk puts ask_user's question in the panel and waits for the answer.
func (a *App) agentAsk(ctx context.Context, question string) (string, error) {
	reply := make(chan string, 1)
	_ = a.callUI(func() {
		a.agent.question = &pendingQuestion{text: question, reply: reply}
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

// slashCommand runs /mode, /new and /provider.
func (a *App) slashCommand(text string) {
	st := &a.agent
	f := strings.Fields(text)
	switch f[0] {
	case "/mode":
		modes := a.modes()
		if len(f) < 2 {
			st.errText = "Modes: " + strings.Join(agent.ModeNames(modes), ", ")
			return
		}
		if _, ok := modes[f[1]]; !ok {
			st.errText = "Unknown mode " + f[1] + ". Modes: " + strings.Join(agent.ModeNames(modes), ", ")
			return
		}
		st.mode, st.errText = f[1], ""
		a.toast("Agent mode: "+f[1], ui.ToastInfo)
	case "/provider":
		if len(f) < 2 {
			st.errText = "Providers: " + strings.Join(a.providerNames(), ", ")
			return
		}
		if _, ok := a.cfg.Current().Agent.Providers[f[1]]; !ok {
			st.errText = "Unknown provider " + f[1]
			return
		}
		st.provider, st.errText = f[1], ""
	case "/new", "/clear":
		a.newAgentSession()
	default:
		st.errText = "Unknown command " + f[0] + ". Try /mode, /provider or /new."
	}
}

func (a *App) newAgentSession() {
	st := &a.agent
	if st.running {
		return
	}
	st.session, st.errText, st.lastCheckpoint = nil, "", nil
	st.calls = map[string]callView{}
	st.todos.Set(nil)
	st.sessionsOpen, st.focusInput = false, true
}

func (a *App) resumeAgentSession(id string) {
	st := &a.agent
	if st.running {
		return
	}
	s, err := st.store.Load(id)
	if err != nil {
		a.fail("Could not open the session", err)
		return
	}
	st.session, st.errText, st.lastCheckpoint = s, "", nil
	st.calls = map[string]callView{}
	if s.Mode != "" {
		st.mode = s.Mode
	}
	if s.Provider != "" {
		st.provider = s.Provider
	}
	st.todos.Set(s.Todos)
	st.sessionsOpen, st.focusInput = false, true
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
	a.agentOpen, a.agent.focusInput = true, true
	if a.agent.input == "" {
		a.agent.input = "@selection "
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
