package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirzakhany/hm/internal/agent/guard"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/search"
)

// script is a provider that answers with canned turns.
type script struct {
	turns    [][]provider.Block
	requests []provider.Request
}

func (s *script) Name(m string) string { return "script:" + m }

func (s *script) Models(context.Context) ([]string, error) { return nil, nil }

func (s *script) Stream(_ context.Context, req provider.Request, on func(provider.Event)) (provider.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.turns) == 0 {
		return provider.Response{}, errors.New("script ran out")
	}
	blocks := s.turns[0]
	s.turns = s.turns[1:]
	stop := provider.StopEnd
	for _, b := range blocks {
		if b.Type == provider.ToolUse {
			stop = provider.StopToolUse
		}
		if b.Type == provider.Text && on != nil {
			on(provider.Event{Kind: provider.EventText, Text: b.Text})
		}
	}
	return provider.Response{Message: provider.Message{Role: provider.Assistant, Blocks: blocks}, StopReason: stop,
		Usage: provider.Usage{Input: 10, Output: 5}}, nil
}

func call(id, name, input string) provider.Block {
	return provider.Block{Type: provider.ToolUse, ToolID: id, ToolName: name, Input: json.RawMessage(input)}
}

func say(s string) provider.Block { return provider.Block{Type: provider.Text, Text: s} }

// diskEnv is a tools.Env over the file system.
type diskEnv struct{ root string }

func (e diskEnv) Root() string                      { return e.root }
func (e diskEnv) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (e diskEnv) Diagnostics(string) []string       { return nil }
func (e diskEnv) Search(ctx context.Context, q search.Query, max int) ([]search.FileResult, error) {
	var out []search.FileResult
	err := search.Run(ctx, e.root, q, search.Options{Engine: "builtin", MaxResults: max}, func(fr search.FileResult) bool {
		out = append(out, fr)
		return true
	})
	return out, err
}

type harness struct {
	agent   *Agent
	prov    *script
	root    string
	asked   []Approval
	answer  func(Approval) Answer
	events  []Event
	session *Session
}

func newAgent(t *testing.T, mode string, turns ...[]provider.Block) *harness {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc A() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, ps := config.Load()
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	m := Modes(cfg.Modes, root)[mode]
	h := &harness{prov: &script{turns: turns}, root: root, answer: func(Approval) Answer { return Answer{Allow: true} }}
	reg := tools.NewRegistry(tools.Builtin(tools.NewShells(), &tools.Todos{})...).Filter(m.Tools)
	h.agent = &Agent{
		Provider: h.prov, Model: "m", System: System(m, root, now(), nil, ""), Tools: reg,
		Policy: NewPolicy(root, m.Approval), Env: diskEnv{root},
		Hooks: Hooks{
			Event: func(e Event) { h.events = append(h.events, e) },
			Approve: func(_ context.Context, a Approval) Answer {
				h.asked = append(h.asked, a)
				return h.answer(a)
			},
			Apply: func(_ context.Context, edits []tools.FileEdit) error {
				for _, e := range edits {
					if e.New == nil {
						if err := os.Remove(e.Path); err != nil {
							return err
						}
						continue
					}
					if err := os.WriteFile(e.Path, e.New, 0o644); err != nil {
						return err
					}
				}
				return nil
			},
		},
	}
	h.session = NewSession(mode, "script")
	return h
}

func (h *harness) run(t *testing.T, input string) {
	t.Helper()
	if err := h.agent.Run(context.Background(), h.session, input); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) results() []provider.Block {
	var out []provider.Block
	for _, m := range h.session.Messages {
		for _, b := range m.Blocks {
			if b.Type == provider.ToolResult {
				out = append(out, b)
			}
		}
	}
	return out
}

func toolNames(req provider.Request) []string {
	var out []string
	for _, t := range req.Tools {
		out = append(out, t.Name)
	}
	return out
}

func TestRubberDuckCannotEdit(t *testing.T) {
	h := newAgent(t, "rubber-duck",
		[]provider.Block{say("I'll just fix it."), call("c1", "edit", `{"path":"a.go","old_string":"return 1","new_string":"return 2"}`)},
		[]provider.Block{call("c2", "write", `{"path":"b.go","content":"x"}`), call("c3", "shell", `{"command":"touch c.go"}`)},
		[]provider.Block{say("I can't edit in this mode; here is the plan.")},
	)
	h.run(t, "change A to return 2")
	for _, name := range toolNames(h.prov.requests[0]) {
		switch name {
		case "edit", "write", "multi_edit", "delete", "shell":
			t.Fatalf("rubber-duck was offered %s", name)
		}
	}
	for _, r := range h.results() {
		if !r.IsError || !strings.Contains(r.Text, "not available in this mode") {
			t.Fatalf("result %+v", r)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(h.root, "a.go")); !strings.Contains(string(data), "return 1") {
		t.Fatal("rubber-duck changed a file")
	}
	for _, p := range []string{"b.go", "c.go"} {
		if _, err := os.Stat(filepath.Join(h.root, p)); err == nil {
			t.Fatalf("rubber-duck created %s", p)
		}
	}
	if len(h.asked) != 0 {
		t.Fatal("nothing should even reach approval")
	}
}

func TestAutoAsksBeforeEveryShellCall(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "shell", `{"command":"echo one"}`), call("c2", "shell", `{"command":"echo two"}`)},
		[]provider.Block{call("c3", "read", `{"path":"a.go"}`), call("c4", "edit", `{"path":"a.go","old_string":"return 1","new_string":"return 2"}`)},
		[]provider.Block{say("done")},
	)
	h.answer = func(a Approval) Answer { return Answer{Allow: a.Plan.Summary == "echo one"} }
	h.run(t, "go")
	if len(h.asked) != 2 || h.asked[0].Plan.Summary != "echo one" || h.asked[1].Plan.Summary != "echo two" {
		t.Fatalf("asked %+v", h.asked)
	}
	rs := h.results()
	if !strings.Contains(rs[0].Text, "one") || rs[0].IsError {
		t.Fatalf("allowed shell %+v", rs[0])
	}
	if !rs[1].IsError || !strings.Contains(rs[1].Text, "rejected") {
		t.Fatalf("rejected shell %+v", rs[1])
	}
	// Reads and edits run without asking in auto.
	if rs[2].IsError || rs[3].IsError || len(h.asked) != 2 {
		t.Fatalf("read/edit %+v %+v", rs[2], rs[3])
	}
	if data, _ := os.ReadFile(filepath.Join(h.root, "a.go")); !strings.Contains(string(data), "return 2") {
		t.Fatal("edit not applied")
	}
	if h.session.Usage.Input != 30 {
		t.Fatalf("usage %+v", h.session.Usage)
	}
}

func TestSessionGrantAndDestructiveGit(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "shell", `{"command":"go version"}`)},
		[]provider.Block{call("c2", "shell", `{"command":"go env GOOS"}`), call("c3", "shell", `{"command":"git push origin main"}`)},
		[]provider.Block{call("c4", "shell", `{"command":"git push origin main"}`)},
		[]provider.Block{say("done")},
	)
	h.answer = func(a Approval) Answer {
		if a.Plan.Category == tools.CatGitPush {
			if a.CanGrant {
				t.Error("destructive git must not offer allow-for-session")
			}
			return Answer{Allow: false, Note: "not now"}
		}
		return Answer{Allow: true, ForSession: true}
	}
	h.run(t, "go")
	// go version asked once and granted; go env runs without asking; both
	// pushes ask.
	var summaries []string
	for _, a := range h.asked {
		summaries = append(summaries, a.Plan.Summary)
	}
	if strings.Join(summaries, "|") != "go version|git push origin main|git push origin main" {
		t.Fatalf("asked %v", summaries)
	}
}

func TestOutsideWorkspaceAsks(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "read", `{"path":"/etc/hosts"}`)},
		[]provider.Block{say("ok")},
	)
	h.answer = func(Approval) Answer { return Answer{Allow: false} }
	h.run(t, "read hosts")
	if len(h.asked) != 1 || !strings.Contains(h.asked[0].Reason, "outside the workspace") {
		t.Fatalf("asked %+v", h.asked)
	}
}

func TestManualEditsReviewHunks(t *testing.T) {
	h := newAgent(t, "lazy",
		[]provider.Block{call("c1", "edit", `{"path":"a.go","old_string":"return 1","new_string":"return 9"}`)},
		[]provider.Block{say("done")},
	)
	// lazy says edit: auto; make edits manual for this test.
	h.agent.Policy.Rules["edit"] = "ask"
	h.answer = func(a Approval) Answer {
		if len(a.Plan.Edits) != 1 {
			t.Fatalf("edit plan %+v", a.Plan)
		}
		e := a.Plan.Edits[0]
		// The user keeps the file as it was but types a note.
		return Answer{Allow: true, Edits: []tools.FileEdit{{Path: e.Path, Old: e.Old, New: []byte("package a\n\nfunc A() int { return 5 }\n")}}}
	}
	h.run(t, "edit")
	if data, _ := os.ReadFile(filepath.Join(h.root, "a.go")); !strings.Contains(string(data), "return 5") {
		t.Fatalf("applied %q", data)
	}
	if r := h.results()[0]; !strings.Contains(r.Text, "only part of the change") {
		t.Fatalf("result %q", r.Text)
	}
}

func TestInvalidToolJSON(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "edit", `{"path":"a.go","old_str`)},
		[]provider.Block{say("retrying")},
	)
	h.run(t, "edit")
	if r := h.results()[0]; !r.IsError || !strings.Contains(r.Text, "INVALID_JSON") {
		t.Fatalf("result %+v", r)
	}
}

func TestSessionStore(t *testing.T) {
	h := newAgent(t, "auto", []provider.Block{say("hello")})
	h.run(t, "hi there")
	st := NewStore(t.TempDir())
	if err := st.Save(h.session); err != nil {
		t.Fatal(err)
	}
	list := st.List()
	if len(list) != 1 || list[0].Title != "hi there" {
		t.Fatalf("list %+v", list)
	}
	s, err := st.Load(list[0].ID)
	if err != nil || len(s.Messages) != 2 || s.Messages[1].TextOf() != "hello" {
		t.Fatalf("loaded %+v %v", s, err)
	}
}

func TestGrantsOutliveOnePolicy(t *testing.T) {
	root := t.TempDir()
	rules := map[string]string{"shell": "ask"}
	plan := tools.Plan{Category: tools.CatShell, GrantKey: "shell:go"}
	first := NewPolicy(root, rules)
	first.Grant("shell", plan)

	next := NewPolicy(root, rules)
	if d, _ := next.Decide("shell", plan); d != Ask {
		t.Fatalf("a fresh policy decided %v, want Ask", d)
	}
	next.Grants = first.Grants
	if d, _ := next.Decide("shell", plan); d != Allow {
		t.Fatalf("shared grants decided %v, want Allow", d)
	}
}

func TestGuardBlocksBeforeAskingAndAsksEveryTime(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "write", `{"path":".env","content":"KEY=1"}`)},
		[]provider.Block{call("c2", "shell", `{"command":"go version && rm -rf /"}`)},
		[]provider.Block{call("c3", "edit", `{"path":"a.go","old_string":"return 1","new_string":"return 2"}`)},
		[]provider.Block{say("done")},
	)
	var r guard.Rules
	r.Paths.Deny = []string{".env"}
	r.Commands.Deny = []string{"rm -rf /*"}
	r.Tools.Ask = []string{"edit"}
	g, err := guard.New(h.root, r)
	if err != nil {
		t.Fatal(err)
	}
	h.agent.Guard = g
	var blocked []string
	h.agent.Hooks.Blocked = func(tool, why string) { blocked = append(blocked, tool) }
	h.answer = func(Approval) Answer { return Answer{Allow: true, ForSession: true} }
	h.run(t, "go")
	if _, err := os.Stat(filepath.Join(h.root, ".env")); err == nil {
		t.Fatal("the guard should have kept .env from being written")
	}
	if strings.Join(blocked, ",") != "write,shell" {
		t.Fatalf("blocked %v", blocked)
	}
	// Edits are auto in this mode; the guard's ask rule makes it ask.
	if len(h.asked) != 1 || h.asked[0].Tool != "edit" {
		t.Fatalf("asked %+v", h.asked)
	}
}
