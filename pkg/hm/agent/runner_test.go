package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/config"
)

// call is a tool call the fake model makes.
type call struct{ name, input string }

// reply is one scripted model response: tool calls, or else text.
type reply struct {
	calls []call
	text  string
}

// fakeModel answers Anthropic streaming requests from a script; once the
// script runs out it answers "done".
type fakeModel struct {
	mu       sync.Mutex
	script   []reply
	requests []string // each request's body
}

func (f *fakeModel) next(body string) reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, body)
	if len(f.script) == 0 {
		return reply{text: "done"}
	}
	r := f.script[0]
	f.script = f.script[1:]
	return r
}

// sent reports whether any request so far contains s.
func (f *fakeModel) sent(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.requests {
		if strings.Contains(b, s) {
			return true
		}
	}
	return false
}

func sse(w http.ResponseWriter, event string, v any) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rep := f.next(string(body))
	w.Header().Set("Content-Type", "text/event-stream")
	sse(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg", "type": "message", "role": "assistant", "model": "test-model", "content": []any{},
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 1}}})
	if len(rep.calls) == 0 {
		sse(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		sse(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": rep.text}})
		sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	}
	for i, c := range rep.calls {
		sse(w, "content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{
			"type": "tool_use", "id": fmt.Sprintf("toolu_%d_%d", len(f.requests), i), "name": c.name, "input": map[string]any{}}})
		sse(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": c.input}})
		sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	stop := "end_turn"
	if len(rep.calls) > 0 {
		stop = "tool_use"
	}
	sse(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 20}})
	sse(w, "message_stop", map[string]any{"type": "message_stop"})
}

// workspace makes a project using a fake model with script, and points
// hm's config and state folders into the test's temp folder.
func workspace(t *testing.T, script []reply, extraConfig string) (string, *fakeModel) {
	t.Helper()
	model := &fakeModel{script: script}
	srv := httptest.NewServer(model)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	oldUser, oldState, oldKeys := config.UserDir, config.StateDir, config.Keychain
	config.UserDir = func() string { return filepath.Join(dir, "config") }
	config.StateDir = func() string { return filepath.Join(dir, "state") }
	config.Keychain = func(string) (string, error) { return "sk-test", nil }
	t.Cleanup(func() { config.UserDir, config.StateDir, config.Keychain = oldUser, oldState, oldKeys })
	root := filepath.Join(dir, "project")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), fmt.Sprintf(`
agent:
  default_provider: test
  providers:
    test: { type: anthropic, base_url: %q, api_key: "keychain:test", model: test-model }
modes:
  careful:
    prompt: builtin:auto
    tools: all
    approval: { edit: ask, shell: ask }
%s`, srv.URL, extraConfig))
	return root, model
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// approverFunc is an Approver from a function.
type approverFunc func(context.Context, Request) (Decision, error)

func (f approverFunc) Approve(ctx context.Context, r Request) (Decision, error) { return f(ctx, r) }

// collect reads r's events until Close.
func collect(r *Runner) func() []Event {
	var mu sync.Mutex
	var out []Event
	done := make(chan struct{})
	ch := r.Events()
	go func() {
		defer close(done)
		for e := range ch {
			mu.Lock()
			out = append(out, e)
			mu.Unlock()
		}
	}()
	return func() []Event {
		_ = r.Close()
		<-done
		return out
	}
}

func results(evs []Event) []string {
	var out []string
	for _, e := range evs {
		if e.Kind == EventToolResult {
			out = append(out, e.Tool+": "+e.Result.Text)
		}
	}
	return out
}

func newRunner(t *testing.T, o Options) *Runner {
	t.Helper()
	r, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestPromptAnswersAndSaves(t *testing.T) {
	root, _ := workspace(t, []reply{{text: "main does nothing"}}, "")
	r := newRunner(t, Options{Root: root})
	events := collect(r)
	if err := r.Prompt(context.Background(), "what does main do?"); err != nil {
		t.Fatal(err)
	}
	if got := r.Answer(); got != "main does nothing" {
		t.Fatalf("answer %q", got)
	}
	info := r.Session()
	if info.Provider != "test" || info.Mode != "auto" || info.Model != "test-model" || !info.Saved || info.Usage.Output == 0 {
		t.Fatalf("session %+v", info)
	}
	evs := events()
	var kinds []string
	for _, e := range evs {
		if e.Session != info.ID {
			t.Fatalf("event of another session: %+v", e)
		}
		if len(kinds) == 0 || kinds[len(kinds)-1] != string(e.Kind) {
			kinds = append(kinds, string(e.Kind))
		}
	}
	if got := strings.Join(kinds, " "); !strings.Contains(got, "text_delta") || !strings.HasSuffix(got, "message turn_end") {
		t.Fatalf("events %s", got)
	}
	if !fileHas(t, config.StateDir(), info.ID) {
		t.Fatal("session not saved")
	}
}

// fileHas reports whether a file in dir is named after id.
func fileHas(t *testing.T, dir, id string) bool {
	t.Helper()
	var found bool
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		found = found || (err == nil && strings.HasPrefix(d.Name(), id))
		return nil
	})
	return found
}

func TestApprovals(t *testing.T) {
	shell := []reply{{calls: []call{{"shell", `{"command":"echo approved-ran"}`}}}}
	cases := []struct {
		name     string
		approver Approver
		want     string
	}{
		{"no approver refuses", nil, "hm runs without a user"},
		{"allowed", approverFunc(func(_ context.Context, r Request) (Decision, error) {
			if r.Tool != "shell" || r.Command != "echo approved-ran" || r.Category != "shell" {
				return Decision{}, fmt.Errorf("request %+v", r)
			}
			return Decision{Allow: true}, nil
		}), "approved-ran"},
		{"denied with a note", approverFunc(func(context.Context, Request) (Decision, error) {
			return Decision{Note: "not today"}, nil
		}), "not today"},
		{"approver error refuses", approverFunc(func(context.Context, Request) (Decision, error) {
			return Decision{}, errors.New("link down")
		}), "link down"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, _ := workspace(t, append([]reply(nil), shell...), "")
			r := newRunner(t, Options{Root: root, Approver: c.approver, NoSession: true})
			events := collect(r)
			if err := r.Prompt(context.Background(), "run it"); err != nil {
				t.Fatal(err)
			}
			res := results(events())
			if len(res) != 1 || !strings.Contains(res[0], c.want) {
				t.Fatalf("results %q, want %q", res, c.want)
			}
		})
	}
}

func TestPartialEditKeepsOnlyPlannedFiles(t *testing.T) {
	root, _ := workspace(t, []reply{{calls: []call{{"write", `{"path":"a.txt","content":"one\n"}`}}}}, "")
	main := filepath.Join(root, "main.go")
	other := filepath.Join(root, "b.txt")
	var seen Request
	ap := approverFunc(func(_ context.Context, r Request) (Decision, error) {
		seen = r
		return Decision{Allow: true, Edits: []Edit{
			{Path: r.Edits[0].Path, New: []byte("changed by the approver\n")},
			{Path: other, New: []byte("not planned\n")},
			{Path: main, New: []byte("not planned\n")},
		}}, nil
	})
	r := newRunner(t, Options{Root: root, Mode: "careful", Approver: ap, NoSession: true})
	if err := r.Prompt(context.Background(), "write a.txt"); err != nil {
		t.Fatal(err)
	}
	if len(seen.Edits) != 1 || filepath.Base(seen.Edits[0].Path) != "a.txt" || string(seen.Edits[0].New) != "one\n" {
		t.Fatalf("request edits %+v", seen.Edits)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "changed by the approver\n" {
		t.Fatalf("a.txt %q", got)
	}
	if _, err := os.Stat(other); err == nil {
		t.Fatal("an edit the plan did not name was written")
	}
	if got, _ := os.ReadFile(main); strings.Contains(string(got), "not planned") {
		t.Fatal("main.go was changed")
	}
}

func TestGuardDenyBeatsApprover(t *testing.T) {
	root, _ := workspace(t, []reply{{calls: []call{{"shell", `{"command":"echo secret"}`}}}}, "")
	writeFile(t, filepath.Join(config.UserDir(), "guard.yaml"), "commands:\n  deny: [\"echo *\"]\n")
	asked := false
	ap := approverFunc(func(context.Context, Request) (Decision, error) { asked = true; return Decision{Allow: true}, nil })
	r := newRunner(t, Options{Root: root, Approver: ap, NoSession: true})
	events := collect(r)
	if err := r.Prompt(context.Background(), "run it"); err != nil {
		t.Fatal(err)
	}
	evs := events()
	if asked {
		t.Fatal("the approver was asked about a denied call")
	}
	blocked := false
	for _, e := range evs {
		blocked = blocked || (e.Kind == EventBlocked && e.Tool == "shell")
	}
	if res := results(evs); !blocked || len(res) != 1 || strings.Contains(res[0], "secret\n") {
		t.Fatalf("blocked %v, results %q", blocked, res)
	}
}

func TestSteerSkipsCallsNotStarted(t *testing.T) {
	root, model := workspace(t, []reply{{calls: []call{
		{"shell", `{"command":"echo first"}`},
		{"shell", `{"command":"echo second"}`},
	}}}, "")
	var r *Runner
	ap := approverFunc(func(_ context.Context, req Request) (Decision, error) {
		if req.Command == "echo first" {
			r.Steer("stop, look at main.go instead")
		}
		return Decision{Allow: true}, nil
	})
	r = newRunner(t, Options{Root: root, Approver: ap, NoSession: true})
	events := collect(r)
	if err := r.Prompt(context.Background(), "run both"); err != nil {
		t.Fatal(err)
	}
	res := results(events())
	if len(res) != 2 || !strings.Contains(res[0], "first") || !strings.Contains(res[1], "Skipped") {
		t.Fatalf("results %q", res)
	}
	if !model.sent("stop, look at main.go instead") {
		t.Fatal("the steering message did not reach the model")
	}
}

func TestFollowUpRunsAfterTheTurn(t *testing.T) {
	root, model := workspace(t, []reply{{text: "first answer"}, {text: "second answer"}}, "")
	r := newRunner(t, Options{Root: root, NoSession: true})
	r.FollowUp("and one more thing")
	if err := r.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if !model.sent("and one more thing") || r.Answer() != "second answer" {
		t.Fatalf("answer %q", r.Answer())
	}
	if err := r.Prompt(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
}

func TestResume(t *testing.T) {
	root, _ := workspace(t, []reply{{text: "remember me"}}, "")
	r := newRunner(t, Options{Root: root})
	if err := r.Prompt(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	id := r.Session().ID
	_ = r.Close()
	if err := r.Prompt(context.Background(), "closed"); !errors.Is(err, ErrClosed) {
		t.Fatalf("prompt after close: %v", err)
	}

	cases := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{"by id", Options{Root: root, SessionID: id}, ""},
		{"continue", Options{Root: root, Continue: true}, ""},
		{"no session", Options{Root: root, Continue: true, NoSession: true}, "without saving"},
		{"other folder", Options{Root: t.TempDir(), SessionID: id}, "run there instead"},
		{"unknown mode", Options{Root: root, Mode: "nope"}, `no mode named "nope"`},
		{"unknown provider", Options{Root: root, Provider: "nope"}, `no provider named "nope"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := New(c.opts)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if r.Session().ID != id || r.Answer() != "remember me" {
				t.Fatalf("resumed %+v %q", r.Session(), r.Answer())
			}
		})
	}
}

func TestSetModelAndMode(t *testing.T) {
	root, model := workspace(t, nil, "")
	r := newRunner(t, Options{Root: root, NoSession: true})
	if err := r.SetModel("", "other-model"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMode("careful"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMode("nope"); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if err := r.Prompt(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if info := r.Session(); info.Model != "other-model" || info.Mode != "careful" || !model.sent(`"other-model"`) {
		t.Fatalf("session %+v", info)
	}
}

func TestSubagentReportsToParent(t *testing.T) {
	root, model := workspace(t, []reply{
		{calls: []call{{"task", `{"description":"look around","prompt":"list the files"}`}}},
		{text: "there is main.go"}, // the sub-agent
		{text: "the sub-agent found main.go"},
	}, "")
	r := newRunner(t, Options{Root: root, NoSession: true})
	events := collect(r)
	if err := r.Prompt(context.Background(), "delegate"); err != nil {
		t.Fatal(err)
	}
	if r.Answer() != "the sub-agent found main.go" || !model.sent("there is main.go") {
		t.Fatalf("answer %q", r.Answer())
	}
	var child string
	for _, e := range events() {
		if e.Kind == EventSubagentEnd {
			child = e.Session
		}
	}
	if child == "" || child == r.Session().ID {
		t.Fatalf("sub-agent session %q", child)
	}
}

func TestBackgroundSubagentReportWakesPrompt(t *testing.T) {
	root, model := workspace(t, []reply{
		{calls: []call{{"task", `{"description":"later","prompt":"say hi","background":true}`}}},
		{text: "started it"},
		{text: "hi from the background"}, // the sub-agent
		{text: "got the report"},
	}, "")
	r := newRunner(t, Options{Root: root, NoSession: true})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.Prompt(ctx, "start one"); err != nil {
		t.Fatal(err)
	}
	if r.Answer() != "got the report" || !model.sent("hi from the background") {
		t.Fatalf("answer %q", r.Answer())
	}
}

func TestBusyAndCancel(t *testing.T) {
	root, _ := workspace(t, []reply{{calls: []call{{"shell", `{"command":"echo x"}`}}}}, "")
	started := make(chan struct{})
	ap := approverFunc(func(ctx context.Context, _ Request) (Decision, error) {
		close(started)
		<-ctx.Done()
		return Decision{}, ctx.Err()
	})
	r := newRunner(t, Options{Root: root, Approver: ap, NoSession: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Prompt(ctx, "run") }()
	<-started
	if err := r.Prompt(context.Background(), "again"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second prompt: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled prompt: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("prompt did not stop")
	}
}
