package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/agent/agenttest"
	"github.com/mirzakhany/hm/internal/config"
)

type (
	call  = agenttest.Call
	reply = agenttest.Reply
)

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
	root, _ := agenttest.Workspace(t, []reply{{Text: "main does nothing"}}, "")
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
	shell := []reply{{Calls: []call{{Name: "shell", Input: `{"command":"echo approved-ran"}`}}}}
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
			root, _ := agenttest.Workspace(t, append([]reply(nil), shell...), "")
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
	root, _ := agenttest.Workspace(t, []reply{{Calls: []call{{Name: "write", Input: `{"path":"a.txt","content":"one\n"}`}}}}, "")
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
	root, _ := agenttest.Workspace(t, []reply{{Calls: []call{{Name: "shell", Input: `{"command":"echo secret"}`}}}}, "")
	agenttest.WriteFile(t, filepath.Join(config.UserDir(), "guard.yaml"), "commands:\n  deny: [\"echo *\"]\n")
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
	root, model := agenttest.Workspace(t, []reply{{Calls: []call{
		{Name: "shell", Input: `{"command":"echo first"}`},
		{Name: "shell", Input: `{"command":"echo second"}`},
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
	if !model.Sent("stop, look at main.go instead") {
		t.Fatal("the steering message did not reach the model")
	}
}

func TestFollowUpRunsAfterTheTurn(t *testing.T) {
	root, model := agenttest.Workspace(t, []reply{{Text: "first answer"}, {Text: "second answer"}}, "")
	r := newRunner(t, Options{Root: root, NoSession: true})
	r.FollowUp("and one more thing")
	if err := r.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if !model.Sent("and one more thing") || r.Answer() != "second answer" {
		t.Fatalf("answer %q", r.Answer())
	}
	if err := r.Prompt(context.Background(), "again"); err != nil {
		t.Fatal(err)
	}
}

func TestResume(t *testing.T) {
	root, _ := agenttest.Workspace(t, []reply{{Text: "remember me"}}, "")
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
	root, model := agenttest.Workspace(t, nil, "")
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
	if info := r.Session(); info.Model != "other-model" || info.Mode != "careful" || !model.Sent(`"other-model"`) {
		t.Fatalf("session %+v", info)
	}
}

func TestSubagentReportsToParent(t *testing.T) {
	root, model := agenttest.Workspace(t, []reply{
		{Calls: []call{{Name: "task", Input: `{"description":"look around","prompt":"list the files"}`}}},
		{Text: "there is main.go"}, // the sub-agent
		{Text: "the sub-agent found main.go"},
	}, "")
	r := newRunner(t, Options{Root: root, NoSession: true})
	events := collect(r)
	if err := r.Prompt(context.Background(), "delegate"); err != nil {
		t.Fatal(err)
	}
	if r.Answer() != "the sub-agent found main.go" || !model.Sent("there is main.go") {
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
	root, model := agenttest.Workspace(t, []reply{
		{Calls: []call{{Name: "task", Input: `{"description":"later","prompt":"say hi","background":true}`}}},
		{Text: "started it"},
		{Text: "hi from the background"}, // the sub-agent
		{Text: "got the report"},
	}, "")
	r := newRunner(t, Options{Root: root, NoSession: true})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.Prompt(ctx, "start one"); err != nil {
		t.Fatal(err)
	}
	if r.Answer() != "got the report" || !model.Sent("hi from the background") {
		t.Fatalf("answer %q", r.Answer())
	}
}

func TestBusyAndCancel(t *testing.T) {
	root, _ := agenttest.Workspace(t, []reply{{Calls: []call{{Name: "shell", Input: `{"command":"echo x"}`}}}}, "")
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
