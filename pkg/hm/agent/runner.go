// Package agent runs hm's coding agent without the editor: from scripts,
// CI, other editors or other Go programs. It reads the same config,
// guard.yaml, skills, MCP servers and instruction files as the app, and
// saves sessions where the app finds them.
//
//	r, err := agent.New(agent.Options{Root: ".", Approver: myApprover{}})
//	if err != nil { ... }
//	defer r.Close()
//	go func() { for e := range r.Events() { ... } }()
//	err = r.Prompt(ctx, "list the packages")
//	fmt.Println(r.Answer())
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/usehm/hm/internal/agent"
	"github.com/usehm/hm/internal/agent/host"
	imgs "github.com/usehm/hm/internal/agent/images"
	"github.com/usehm/hm/internal/agent/mcp"
	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/agent/tools"
	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/secrets"
)

// Options configure a Runner. Every field may be left empty.
type Options struct {
	// Root is the workspace the agent works in; empty is the current
	// folder.
	Root string
	// UserConfig and ProjectConfig are the config files; empty are
	// ~/.config/hm/config.yaml and Root/.hm/config.yaml.
	UserConfig, ProjectConfig string
	// Mode, Provider and Model override agent.default_mode,
	// agent.default_provider and the provider's model.
	Mode, Provider, Model string
	// SessionID resumes a saved session; Continue resumes the newest one
	// of Root, or starts one when there is none.
	SessionID string
	Continue  bool
	// NoSession keeps the session in memory only.
	NoSession bool
	// MaxSteps bounds the model calls of one turn, sub-agents' each; 0 is
	// 80. A turn that uses them up ends with ErrMaxSteps.
	MaxSteps int
	// Approver decides the calls that need approval; nil refuses them.
	Approver Approver
	// Logger gets warnings, such as a config problem or a skill that does
	// not read; nil drops them.
	Logger *slog.Logger
}

// Runner runs turns of one session. Its methods are safe to call from
// several goroutines; one turn runs at a time.
type Runner struct {
	opts   Options
	log    *slog.Logger
	h      *host.Host
	store  *agent.Store // nil with NoSession
	bus    *agent.Bus
	ictx   *agent.Context // the instruction files, shared with sub-agents
	top    *Runner        // the Runner New returned; itself for that one
	parent *Runner        // nil but for a sub-agent
	depth  int
	write  bool // a sub-agent may edit and run commands

	shells *tools.Shells
	todos  *tools.Todos
	grants *agent.Grants
	subSem chan struct{} // bounds this agent's sub-agents running at once
	bg     atomic.Int32  // background sub-agents whose report is due

	mu                    sync.Mutex
	session               *agent.Session
	mode, provider, model string
	effort                string // empty for the provider's
	running               bool
	cancel                context.CancelFunc
	steer, follow         []agent.Input
	resumed               bool // the session was saved before New

	// For the top Runner only: base ends on Close, unblocking emit;
	// bgCtx ends background sub-agents on Close or Abort. events is nil until
	// Events is called.
	base      context.Context
	stop      context.CancelFunc
	bgCtx     context.Context // guarded by mu
	bgStop    context.CancelFunc
	evMu      sync.RWMutex
	events    chan Event
	closed    bool
	closeOnce sync.Once
}

// New opens a session: the one Options names, or a new one.
func New(o Options) (*Runner, error) {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	root := o.Root
	if root == "" {
		var err error
		if root, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", root)
	}
	if o.NoSession && (o.SessionID != "" || o.Continue) {
		return nil, errors.New("a session cannot be resumed without saving it")
	}
	if config.Keychain == nil {
		config.Keychain = secrets.OS{}.Get
	}
	user, proj := o.UserConfig, o.ProjectConfig
	if user == "" {
		user = config.UserPath()
	}
	if proj == "" {
		proj = config.ProjectPath(root)
	}
	cm := config.NewManager(user, proj)
	cfg := cm.Current()
	for _, p := range cm.Problems() {
		log.Warn("config: " + p.Error())
	}
	cm.Close()

	projects, err := agent.LoadProjects(config.StateDir())
	if err != nil {
		log.Warn("Could not read the agent projects: " + err.Error())
	}
	if err := host.MaterializeSkills(); err != nil {
		log.Warn("Could not write the built-in skills: " + err.Error())
	}
	r := &Runner{opts: o, log: log, bus: agent.NewBus(nil)}
	r.top = r
	r.h = &host.Host{Root: root, Config: cfg, Projects: projects, Warn: func(msg string) { log.Warn(msg) }}
	r.base, r.stop = context.WithCancel(context.Background())
	r.bgCtx, r.bgStop = context.WithCancel(r.base)
	if !o.NoSession {
		r.store = agent.NewStore(config.StateDir())
	}
	s, err := r.openSession(root, cfg)
	if err != nil {
		return nil, err
	}
	r.mode, r.provider, r.model = cfg.Agent.DefaultMode, cfg.Agent.DefaultProvider, o.Model
	if s.Mode != "" {
		r.mode = s.Mode
	}
	if s.Provider != "" {
		r.provider = s.Provider
	}
	if o.Mode != "" {
		r.mode = o.Mode
	}
	if o.Provider != "" {
		r.provider = o.Provider
	}
	if err := r.check(r.mode, r.provider); err != nil {
		return nil, err
	}
	if len(cfg.Tools.MCP) > 0 {
		r.h.MCP = mcp.NewManager(root)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		r.h.MCP.Apply(ctx, cfg.Tools.MCP)
		cancel()
		for _, sv := range r.h.MCP.Servers() {
			if sv.Err != nil {
				log.Warn("MCP: " + sv.Err.Error())
			}
		}
	}
	r.ictx = r.h.Context()
	for _, w := range r.ictx.Warnings {
		log.Warn("Agent instructions: " + w)
	}
	r.init(s)
	return r, nil
}

// openSession resumes the session Options name, or starts one.
func (r *Runner) openSession(root string, cfg config.Config) (*agent.Session, error) {
	o := r.opts
	id := o.SessionID
	if id == "" && o.Continue {
		for _, sum := range r.store.List() {
			if sum.Root == root && sum.ParentID == "" {
				id = sum.ID
				break
			}
		}
	}
	if id == "" {
		s := agent.NewSession(cfg.Agent.DefaultMode, cfg.Agent.DefaultProvider)
		s.Root = root
		return s, nil
	}
	s, err := r.store.Load(id)
	if err != nil {
		return nil, fmt.Errorf("could not open session %s: %w", id, err)
	}
	if s.Root != "" && s.Root != root {
		return nil, fmt.Errorf("session %s works in %s; run there instead", id, s.Root)
	}
	host.Recover(r.store, s)
	r.resumed = true
	return s, nil
}

// init sets up r to run session s.
func (r *Runner) init(s *agent.Session) {
	r.session = s
	r.shells, r.todos, r.grants = tools.NewShells(), &tools.Todos{}, agent.NewGrants()
	r.todos.Set(s.Todos)
	r.subSem = make(chan struct{}, max(1, r.h.Config.Agent.Subagents.MaxConcurrent))
	parent, name := "", s.Title
	if r.parent != nil {
		parent = r.parent.session.ID
	}
	if name == "" {
		name = "session " + s.ID
	}
	r.bus.Register(s.ID, name, parent)
}

// check reports a mode or provider the config does not have.
func (r *Runner) check(mode, prov string) error {
	if _, ok := r.h.Modes()[mode]; !ok {
		return fmt.Errorf("no mode named %q; the config has %s", mode, strings.Join(agent.ModeNames(r.h.Modes()), ", "))
	}
	if _, ok := r.h.Config.Agent.Providers[prov]; !ok {
		return fmt.Errorf("no provider named %q in agent.providers", prov)
	}
	return nil
}

// Prompt sends text, with any images, and works until the agent has
// nothing left to do: the turn, the messages queued with Steer and
// FollowUp, and the reports of sub-agents it started in the background.
// Cancelling ctx stops it. An image that is not PNG, JPEG, GIF or WebP is
// an error; a large one is scaled down.
func (r *Runner) Prompt(ctx context.Context, text string, images ...Image) error {
	in, err := input(text, images)
	if err != nil {
		return err
	}
	ctx, err = r.begin(ctx)
	if err != nil {
		return err
	}
	defer r.end()
	for {
		err := r.turn(ctx, in)
		end := Event{Kind: EventTurnEnd, Session: r.session.ID}
		if err != nil {
			end.Text = err.Error()
		}
		r.emit(end)
		if err != nil {
			return err
		}
		in = agent.Join(append(r.inbox(false), r.inbox(true)...))
		for in.Empty() && r.bg.Load() > 0 {
			if m, ok := r.bus.Wait(ctx, r.session.ID, "", time.Minute); ok {
				in = agent.Input{Text: host.MailText(m)}
			} else if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		if in.Empty() {
			return nil
		}
	}
}

// begin marks a turn running, unless one is.
func (r *Runner) begin(ctx context.Context) (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.top.isClosed() {
		return nil, ErrClosed
	}
	if r.running {
		return nil, ErrBusy
	}
	ctx, cancel := context.WithCancel(ctx)
	r.running, r.cancel = true, cancel
	return ctx, nil
}

func (r *Runner) end() {
	r.mu.Lock()
	cancel := r.cancel
	r.running, r.cancel = false, nil
	r.mu.Unlock()
	cancel()
}

// turn runs one turn of the agent on in.
func (r *Runner) turn(ctx context.Context, in agent.Input) error {
	ag, err := r.build()
	if err != nil {
		return err
	}
	s := r.session
	s.SetStatus(agent.StatusRunning)
	r.bus.SetRunning(s.ID, true)
	err = ag.Run(ctx, s, in.Text, in.Images...)
	s.SetTodos(r.todos.Items())
	s.SetStatus(agent.StatusIdle)
	r.bus.SetRunning(s.ID, false)
	r.save()
	return err
}

// build makes the agent for r's next turn.
func (r *Runner) build() (*agent.Agent, error) {
	r.mu.Lock()
	mode, prov, model, effort := r.mode, r.provider, r.model, r.effort
	r.mu.Unlock()
	s := r.session
	return r.h.Build(host.Turn{
		Session: s, Mode: mode, Provider: prov, Model: model, Effort: effort, Grants: r.grants, MaxSteps: r.opts.MaxSteps,
		Env: diskEnv{root: r.h.Root, search: r.h.Config.Search}, Context: r.top.ictx,
		Tools: host.ToolsetOptions{Shells: r.shells, Todos: r.todos, Agents: link{r},
			Spawn: r.depth < r.h.Config.Agent.Subagents.MaxDepth, Subagent: r.parent != nil, Write: r.write},
		Hooks: agent.Hooks{
			Event: func(e agent.Event) {
				ev := event(e, s.ID)
				if e.Kind == agent.EvMessage {
					if m, ok := s.Last(); ok {
						ev.Message = message(m)
					}
				}
				r.top.emit(ev)
			},
			Approve: r.approve,
			Ask:     r.ask,
			Apply:   func(_ context.Context, edits []tools.FileEdit) error { return applyEdits(edits) },
			Blocked: func(tool, why string) {
				r.log.Warn("guard blocked " + tool + ": " + why)
				r.top.emit(Event{Kind: EventBlocked, Session: s.ID, Tool: tool, Text: why})
			},
			Steer:    func() []agent.Input { return r.inbox(false) },
			FollowUp: func() []agent.Input { return r.inbox(true) },
			Persist:  r.save,
		},
	})
}

func (r *Runner) save() {
	if r.store == nil {
		return
	}
	if err := r.store.Save(r.session); err != nil {
		r.log.Warn("Could not save the agent session: " + err.Error())
	}
}

// Steer sends text, with any images, to the running turn: it joins after
// the tool call in progress, and the calls not yet started are skipped.
// Sent while idle, it waits for the next Prompt. Images are checked as
// Prompt checks them.
func (r *Runner) Steer(text string, images ...Image) error { return r.queue(text, images, false) }

// FollowUp sends text, with any images, to be answered once the running
// turn ends.
func (r *Runner) FollowUp(text string, images ...Image) error { return r.queue(text, images, true) }

func (r *Runner) queue(text string, images []Image, followUp bool) error {
	in, err := input(text, images)
	if err != nil || in.Empty() {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if followUp {
		r.follow = append(r.follow, in)
	} else {
		r.steer = append(r.steer, in)
	}
	return nil
}

// input is a message of text and images, the images fitted for the APIs.
func input(text string, images []Image) (agent.Input, error) {
	in := agent.Input{Text: text}
	for i, im := range images {
		d, err := imgs.Prepare(im.Data)
		if err != nil {
			return agent.Input{}, fmt.Errorf("image %d: %w", i+1, err)
		}
		in.Images = append(in.Images, d)
	}
	return in, nil
}

// inbox takes the follow-ups, or the steering messages, waiting for the
// agent: the host's, then other agents' mail.
func (r *Runner) inbox(followUp bool) []agent.Input {
	r.mu.Lock()
	var out []agent.Input
	if followUp {
		out, r.follow = r.follow, nil
	} else {
		out, r.steer = r.steer, nil
	}
	r.mu.Unlock()
	for _, m := range r.bus.Take(r.session.ID, followUp) {
		out = append(out, agent.Input{Text: host.MailText(m)})
	}
	return out
}

// Events streams what happens in the Runner's turns and its sub-agents'.
// Once it is called, the channel must be read: a full channel stalls the
// agent. It is closed by Close.
func (r *Runner) Events() <-chan Event {
	t := r.top
	t.evMu.Lock()
	defer t.evMu.Unlock()
	if t.events == nil {
		t.events = make(chan Event, 256)
		if t.closed {
			close(t.events)
		}
	}
	return t.events
}

// emit sends e to Events, if anyone asked for them.
func (r *Runner) emit(e Event) {
	r.evMu.RLock()
	defer r.evMu.RUnlock()
	if r.events == nil || r.closed {
		return
	}
	select {
	case r.events <- e:
	case <-r.base.Done():
	}
}

func (r *Runner) isClosed() bool {
	r.evMu.RLock()
	defer r.evMu.RUnlock()
	return r.closed
}

// Session describes the session.
func (r *Runner) Session() SessionInfo {
	r.mu.Lock()
	mode, prov, model, effort, running := r.mode, r.provider, r.model, r.effort, r.running
	r.mu.Unlock()
	s := r.session
	u := s.UsageNow()
	if model == "" {
		model = r.h.Config.Agent.Providers[prov].Model
	}
	if effort == "" {
		effort = r.h.Config.Agent.Providers[prov].Effort
	}
	return SessionInfo{
		ID: s.ID, Title: s.Title, Root: s.Root, Mode: mode, Provider: prov, Model: model, Effort: effort,
		Status: s.StatusNow(), Context: s.ContextUsed(), Saved: r.store != nil, Resumed: r.resumed, Running: running,
		Usage: Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Cost: u.Cost},
	}
}

// Answer is the agent's last message: what a turn ended with.
func (r *Runner) Answer() string { return host.LastAssistantText(r.session.Snapshot()) }

// SetModel switches the provider and its model for the next turn. An
// empty provider keeps the current one; an empty model is the provider's
// configured one.
func (r *Runner) SetModel(prov, model string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if prov == "" {
		prov = r.provider
	}
	if err := r.check(r.mode, prov); err != nil {
		return err
	}
	r.provider, r.model = prov, model
	return nil
}

// SetEffort sets how hard the model thinks from the next turn: one of
// provider.Efforts ("off" turns thinking off), or empty for the
// provider's effort.
func (r *Runner) SetEffort(effort string) error {
	if effort != "" && !slices.Contains(provider.Efforts, effort) {
		return fmt.Errorf("unknown effort %q; use one of %s", effort, strings.Join(provider.Efforts, ", "))
	}
	r.mu.Lock()
	r.effort = effort
	r.mu.Unlock()
	return nil
}

// SetMode switches the mode for the next turn.
func (r *Runner) SetMode(mode string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(mode, r.provider); err != nil {
		return err
	}
	r.mode = mode
	return nil
}

// Compact summarises the session's older messages, keeping what focus
// names. It returns ErrNothingToCompact for a short session.
func (r *Runner) Compact(ctx context.Context, focus string) error {
	ctx, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer r.end()
	ag, err := r.build()
	if err != nil {
		return err
	}
	err = ag.Compact(ctx, r.session, focus)
	r.save()
	return err
}

// background is the context background sub-agents run under.
func (r *Runner) background() context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bgCtx
}

// Abort stops the turn in progress and the background sub-agents; the
// Runner stays open for the next Prompt. Queued messages are dropped.
func (r *Runner) Abort() {
	t := r.top
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
	}
	t.bgStop()
	t.bgCtx, t.bgStop = context.WithCancel(t.base)
	t.steer, t.follow = nil, nil
	t.mu.Unlock()
}

// Close stops the turn in progress, background sub-agents, shells and MCP
// servers, saves the session and closes Events.
func (r *Runner) Close() error {
	t := r.top
	t.closeOnce.Do(func() {
		t.mu.Lock()
		if t.cancel != nil {
			t.cancel()
		}
		t.mu.Unlock()
		t.stop() // unblocks emit, ends background sub-agents
		t.evMu.Lock()
		t.closed = true
		if t.events != nil {
			close(t.events)
		}
		t.evMu.Unlock()
		t.shells.StopAll()
		if t.h.MCP != nil {
			t.h.MCP.Close()
		}
		t.save()
	})
	return nil
}
