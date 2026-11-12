// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package agentcmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/usehm/hm/pkg/hm/agent"
)

// approveAsk sends calls that need approval to the RPC client; only
// --rpc takes it, and it is its default.
const approveAsk = "ask"

// rpcCommand is one line of input. Each type reads the fields it needs;
// docs/rpc.md lists them.
type rpcCommand struct {
	ID   any    `json:"id"`
	Type string `json:"type"`

	Text        string     `json:"text"`
	Images      []rpcImage `json:"images"`
	CallID      string     `json:"call_id"`
	Allow       bool       `json:"allow"`
	ForSession  bool       `json:"for_session"`
	Note        string     `json:"note"`
	Edits       []rpcEdit  `json:"edits"`
	Provider    string     `json:"provider"`
	Model       string     `json:"model"`
	Effort      *string    `json:"effort"`
	Mode        string     `json:"mode"`
	Focus       string     `json:"focus"`
	Entry       string     `json:"entry"`
	Format      string     `json:"format"`
	Path        string     `json:"path"`
	AllBranches bool       `json:"all_branches"`
	All         bool       `json:"all"`
}

// rpcImage is a picture sent with prompt, steer or follow_up.
type rpcImage struct {
	Data []byte `json:"data"` // base64; the type is read from the data
}

// images are the command's pictures, checked and fitted for the model.
func (c rpcCommand) images() ([]agent.Image, error) {
	var out []agent.Image
	for i, im := range c.Images {
		ai, err := agent.LoadImage(im.Data)
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i+1, err)
		}
		out = append(out, ai)
	}
	return out, nil
}

// rpcEdit is a file an approval keeps, with its whole new content.
type rpcEdit struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// responseLine answers a command.
type responseLine struct {
	envelope
	ID      any    `json:"id"`
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Data    any    `json:"data,omitempty"`
}

func (w *jsonWriter) response(session string, c rpcCommand, data any, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	l := &responseLine{ID: c.ID, Command: c.Type, OK: err == nil, Data: data}
	if err != nil {
		l.Error, l.Data = err.Error(), nil
	}
	w.write("response", session, l)
}

// rpcClient approves calls and answers questions for the agent by asking
// the client: it waits for the approve or answer command naming the
// call. A call is announced once its approval_needed or question line is
// written, so the client cannot answer before it can know the call.
type rpcClient struct {
	auto string // "ask", or "edits" or "all" to allow those without asking

	mu        sync.Mutex
	announced map[string]string // call id → "approval" or "question", not yet answered
	replies   map[string]chan any
}

func newRPCClient(auto string) *rpcClient {
	return &rpcClient{auto: auto, announced: map[string]string{}, replies: map[string]chan any{}}
}

// reply is the channel call id's answer comes on.
func (c *rpcClient) reply(id string) chan any {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.replies[id]
	if ch == nil {
		ch = make(chan any, 1)
		c.replies[id] = ch
	}
	return ch
}

// done forgets call id.
func (c *rpcClient) done(id string) {
	c.mu.Lock()
	delete(c.announced, id)
	delete(c.replies, id)
	c.mu.Unlock()
}

func (c *rpcClient) announce(id, kind string) {
	c.mu.Lock()
	c.announced[id] = kind
	c.mu.Unlock()
}

// deliver hands v to the waiting call id, which must be announced as kind.
func (c *rpcClient) deliver(id, kind string, v any) error {
	c.mu.Lock()
	if c.announced[id] != kind {
		c.mu.Unlock()
		return fmt.Errorf("no %s is waiting for call %q", kind, id)
	}
	delete(c.announced, id)
	c.mu.Unlock()
	c.reply(id) <- v
	return nil
}

// pending lists the calls waiting for the client, by kind.
func (c *rpcClient) pending(kind string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []string{}
	for id, k := range c.announced {
		if k == kind {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func (c *rpcClient) Approve(ctx context.Context, r agent.Request) (agent.Decision, error) {
	if c.auto == approveAll || (c.auto == approveEdits && r.Category == "edit") {
		return agent.Decision{Allow: true}, nil
	}
	defer c.done(r.CallID)
	select {
	case v := <-c.reply(r.CallID):
		return v.(agent.Decision), nil
	case <-ctx.Done():
		return agent.Decision{Note: "the turn was stopped"}, nil
	}
}

func (c *rpcClient) Ask(ctx context.Context, q string) (string, error) {
	id := agent.CallID(ctx)
	defer c.done(id)
	select {
	case v := <-c.reply(id):
		return v.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// rpcSession is the session info get_state and fork answer with.
type rpcSession struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Root     string      `json:"root"`
	Mode     string      `json:"mode"`
	Provider string      `json:"provider"`
	Model    string      `json:"model"`
	Effort   string      `json:"effort,omitempty"`
	Status   string      `json:"status"`
	Running  bool        `json:"running"`
	Saved    bool        `json:"saved"`
	Context  int64       `json:"context"`
	Usage    usageFields `json:"usage"`
}

func sessionOf(i agent.SessionInfo) rpcSession {
	return rpcSession{ID: i.ID, Title: i.Title, Root: i.Root, Mode: i.Mode, Provider: i.Provider, Model: i.Model, Effort: i.Effort,
		Status: i.Status, Running: i.Running, Saved: i.Saved, Context: i.Context, Usage: usageOf(i.Usage)}
}

// rpc runs `hm agent --rpc`: commands from stdin, events and responses
// on stdout, until stdin ends.
type rpc struct {
	r      *agent.Runner
	w      *jsonWriter
	client *rpcClient
	// busy is set while a prompt or compact runs; work waits for them.
	mu   sync.Mutex
	busy bool
	work sync.WaitGroup
}

func runRPC(ctx context.Context, o options, stdin io.Reader, stdout, stderr io.Writer) int {
	client := newRPCClient(o.approve)
	r, err := newRunner(o, client, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "hm agent:", err)
		return exitUsage
	}
	p := &rpc{r: r, w: newJSONWriter(stdout), client: client}
	p.w.sessionStart(r.Session(), "")
	shown := make(chan struct{})
	go func() {
		defer close(shown)
		for e := range r.Events() {
			switch e.Kind {
			case agent.EventApprovalNeeded:
				client.announce(e.CallID, "approval")
			case agent.EventQuestion:
				client.announce(e.CallID, "question")
			case agent.EventApproval:
				client.done(e.CallID) // decided without the client
			}
			p.w.event(e)
		}
	}()

	lines := make(chan []byte)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdin)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()
	code := exitOK
loop:
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				break loop
			}
			p.handle(ctx, l)
		case <-ctx.Done():
			r.Abort()
			code = exitInterrupted
			break loop
		}
	}
	p.work.Wait()
	_ = r.Close()
	<-shown
	answer := r.Answer()
	p.w.sessionEnd(r.Session(), answer, code, nil)
	return code
}

// handle runs one line of input.
func (p *rpc) handle(ctx context.Context, line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var c rpcCommand
	if err := json.Unmarshal(line, &c); err != nil {
		var idOnly struct {
			ID any `json:"id"`
		}
		_ = json.Unmarshal(line, &idOnly)
		p.respond(rpcCommand{ID: idOnly.ID}, nil, fmt.Errorf("not a JSON command: %v", err))
		return
	}
	data, err := p.run(ctx, c)
	if c.Type == "prompt" || c.Type == "compact" {
		if err != nil {
			p.respond(c, nil, err)
		}
		return // answered by the work itself
	}
	p.respond(c, data, err)
}

func (p *rpc) respond(c rpcCommand, data any, err error) {
	p.w.response(p.r.Session().ID, c, data, err)
}

// start runs fn in the background as the one prompt or compact. With
// accept, c is answered first, so its response comes before the work's
// events.
func (p *rpc) start(c rpcCommand, accept bool, fn func()) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy {
		return agent.ErrBusy
	}
	p.busy = true
	if accept {
		p.respond(c, nil, nil)
	}
	p.work.Add(1)
	go func() {
		defer p.work.Done()
		defer func() { p.mu.Lock(); p.busy = false; p.mu.Unlock() }()
		fn()
	}()
	return nil
}

func (p *rpc) idle() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy {
		return agent.ErrBusy
	}
	return nil
}

// run does command c; prompt and compact answer for themselves.
func (p *rpc) run(ctx context.Context, c rpcCommand) (any, error) {
	r := p.r
	switch c.Type {
	case "prompt":
		if c.Text == "" && len(c.Images) == 0 {
			return nil, errors.New("prompt needs text")
		}
		ims, err := c.images()
		if err != nil {
			return nil, err
		}
		// The turn reports its end in turn_end; Ctrl-C aborts it.
		return nil, p.start(c, true, func() { _ = r.Prompt(context.WithoutCancel(ctx), c.Text, ims...) })
	case "compact":
		return nil, p.start(c, false, func() {
			err := r.Compact(context.WithoutCancel(ctx), c.Focus)
			p.respond(c, nil, err)
		})
	case "steer", "follow_up":
		if c.Text == "" && len(c.Images) == 0 {
			return nil, errors.New(c.Type + " needs text")
		}
		ims, err := c.images()
		if err != nil {
			return nil, err
		}
		if c.Type == "steer" {
			return nil, r.Steer(c.Text, ims...)
		}
		return nil, r.FollowUp(c.Text, ims...)
	case "abort":
		r.Abort()
		return nil, nil
	case "approve":
		d := agent.Decision{Allow: c.Allow, ForSession: c.ForSession, Note: c.Note}
		if c.Edits != nil {
			d.Edits = []agent.Edit{}
			for _, e := range c.Edits {
				path := e.Path
				if !filepath.IsAbs(path) {
					path = filepath.Join(r.Session().Root, path)
				}
				d.Edits = append(d.Edits, agent.Edit{Path: path, New: []byte(e.Content)})
			}
		}
		return nil, p.client.deliver(c.CallID, "approval", d)
	case "answer":
		if c.Text == "" {
			return nil, errors.New("answer needs text")
		}
		return nil, p.client.deliver(c.CallID, "question", c.Text)
	case "set_model":
		if c.Effort != nil {
			if err := r.SetEffort(*c.Effort); err != nil {
				return nil, err
			}
		}
		if c.Provider == "" && c.Model == "" && c.Effort != nil {
			return nil, nil // only the effort changes
		}
		return nil, r.SetModel(c.Provider, c.Model)
	case "set_mode":
		return nil, r.SetMode(c.Mode)
	case "get_state":
		return map[string]any{
			"session":           sessionOf(r.Session()),
			"running":           p.idle() != nil,
			"pending_approvals": p.client.pending("approval"),
			"pending_questions": p.client.pending("question"),
		}, nil
	case "list_sessions":
		return map[string]any{"sessions": sessionsOf(r.Sessions(c.All))}, nil
	case "fork":
		if err := p.idle(); err != nil {
			return nil, err
		}
		info, err := r.Fork(c.Entry)
		if err != nil {
			return nil, err
		}
		p.w.sessionStart(info, "")
		return sessionOf(info), nil
	case "export":
		return p.export(c)
	case "":
		return nil, errors.New("the command has no type")
	}
	return nil, fmt.Errorf("unknown command %q", c.Type)
}

// export writes the session to a file, secrets taken out.
func (p *rpc) export(c rpcCommand) (any, error) {
	format := agent.ExportFormat(c.Format)
	if format == "" {
		format = agent.ExportHTML
	}
	data, redacted, err := p.r.Export(format, c.AllBranches)
	if err != nil {
		return nil, err
	}
	path := p.r.ExportPath(c.Path, format)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	if redacted == nil {
		redacted = []string{}
	}
	return map[string]any{"path": path, "redacted": redacted}, nil
}

// rpcSummary is a saved session in list_sessions.
type rpcSummary struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Mode     string    `json:"mode"`
	Provider string    `json:"provider"`
	Root     string    `json:"root"`
	ParentID string    `json:"parent_id,omitempty"`
	ForkOf   string    `json:"fork_of,omitempty"`
	Status   string    `json:"status"`
	Updated  time.Time `json:"updated"`
	Turns    int       `json:"turns"`
	Cost     float64   `json:"cost"`
}

func sessionsOf(list []agent.SessionSummary) []rpcSummary {
	out := []rpcSummary{}
	for _, s := range list {
		out = append(out, rpcSummary{ID: s.ID, Title: s.Title, Mode: s.Mode, Provider: s.Provider, Root: s.Root,
			ParentID: s.ParentID, ForkOf: s.ForkOf, Status: s.Status, Updated: s.Updated, Turns: s.Turns, Cost: s.Cost})
	}
	return out
}
