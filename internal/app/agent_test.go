package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/workspace"
)

// toolCall is a scripted call the fake model makes.
type toolCall struct {
	name  string
	input string
}

// fakeModel serves scripted turns in Anthropic or OpenAI streaming format.
type fakeModel struct {
	mu       sync.Mutex
	turns    [][]toolCall // a turn with no calls answers "done"
	requests []map[string]any
	// limited is how many more requests get a 429 asking for a 1s wait.
	limited int
}

// rateLimit answers a 429 while f.limited lasts.
func (f *fakeModel) rateLimit(w http.ResponseWriter) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.limited == 0 {
		return false
	}
	f.limited--
	w.Header().Set("Retry-After", "1")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	return true
}

func (f *fakeModel) next(body map[string]any) []toolCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, body)
	if len(f.turns) == 0 {
		return nil
	}
	t := f.turns[0]
	f.turns = f.turns[1:]
	return t
}

func sse(w http.ResponseWriter, event string, v any) {
	data, _ := json.Marshal(v)
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func (f *fakeModel) anthropic(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	calls := f.next(body)
	w.Header().Set("Content-Type", "text/event-stream")
	sse(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg", "type": "message", "role": "assistant", "model": "claude-opus-5-5", "content": []any{},
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 1}}})
	i := 0
	if len(calls) == 0 {
		sse(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		sse(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Done: main prints 1."}})
		sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	}
	for _, c := range calls {
		sse(w, "content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{
			"type": "tool_use", "id": fmt.Sprintf("toolu_%d_%d", len(f.requests), i), "name": c.name, "input": map[string]any{}}})
		sse(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": c.input}})
		sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
		i++
	}
	stop := "end_turn"
	if len(calls) > 0 {
		stop = "tool_use"
	}
	sse(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 20}})
	sse(w, "message_stop", map[string]any{"type": "message_stop"})
}

func (f *fakeModel) openai(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	calls := f.next(body)
	w.Header().Set("Content-Type", "text/event-stream")
	if len(calls) == 0 {
		sse(w, "", map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "Done: main prints 1."}}}})
	}
	for i, c := range calls {
		sse(w, "", map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": i, "id": fmt.Sprintf("call_%d_%d", len(f.requests), i), "type": "function",
				"function": map[string]any{"name": c.name, "arguments": c.input}}}}}}})
	}
	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	sse(w, "", map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}})
	sse(w, "", map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "cost": 0.001}})
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// sameTask is the task both providers run: edit main.go, then run a check.
func sameTask() [][]toolCall {
	return [][]toolCall{
		{{"read", `{"path":"main.go"}`}},
		{{"edit", `{"path":"main.go","old_string":"func main() {}","new_string":"func main() { println(1) }"}`},
			{"shell", `{"command":"echo checked"}`}},
		nil,
	}
}

func agentProject(t *testing.T, kind string, model *fakeModel) (*harness, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"test-model"},{"id":"other-model"}]}`)
			return
		}
		if model.rateLimit(w) {
			return
		}
		if kind == "anthropic" {
			model.anthropic(w, r)
		} else {
			model.openai(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	root := project(t)
	base := srv.URL
	if kind != "anthropic" {
		base += "/api/v1"
	}
	typ := "openai"
	if kind == "anthropic" {
		typ = "anthropic"
	}
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), fmt.Sprintf(`
agent:
  default_provider: test
  providers:
    test: { type: %s, base_url: %q, api_key: "keychain:test", model: test-model }
`, typ, base))
	h := newHarness(t, root, false)
	h.app.agent.keys.Set("test", "sk-test")
	h.app.refreshProviderKeys()
	h.app.agentOpen = true
	h.frame()
	return h, root
}

func runTask(t *testing.T, h *harness, text string) {
	t.Helper()
	h.app.run().input = text
	h.app.sendAgent()
	if h.app.run().errText != "" {
		t.Fatal(h.app.run().errText)
	}
}

func TestSameTaskOnAnthropicAndOpenRouter(t *testing.T) {
	for _, kind := range []string{"anthropic", "openai"} {
		t.Run(kind, func(t *testing.T) {
			model := &fakeModel{turns: sameTask()}
			h, root := agentProject(t, kind, model)
			main := filepath.Join(root, "main.go")
			h.app.openFile(main) // the edit should land in the open buffer
			runTask(t, h, "make main print 1 @main.go")
			h.until("shell approval", 10*time.Second, func() bool { return h.app.run().approval != nil })
			p := h.app.run().approval
			if p.req.Tool != "shell" || p.req.Plan.Summary != "echo checked" {
				t.Fatalf("approval %+v", p.req)
			}
			// The edit ran without asking (auto mode) and is in the buffer and on disk.
			if got := string(h.app.bufs.Get(main).Bytes()); !strings.Contains(got, "println(1)") {
				t.Fatalf("buffer %q", got)
			}
			if got, _ := os.ReadFile(main); !strings.Contains(string(got), "println(1)") {
				t.Fatalf("disk %q", got)
			}
			h.app.answerApproval(h.app.run(), true, false)
			h.until("turn end", 10*time.Second, func() bool { return !h.app.run().running })
			s := h.app.run().session
			if last := s.Messages[len(s.Messages)-1]; last.TextOf() != "Done: main prints 1." {
				t.Fatalf("last message %+v", last)
			}
			if u := s.UsageNow(); u.Input == 0 || u.Output == 0 {
				t.Fatalf("usage %+v", s.Usage)
			}
			if s.ContextUsed() == 0 || h.app.contextMeter(h.app.run()) == nil {
				t.Fatal("no context meter after a model call")
			}
			// The model saw the shell output, and the @main.go context.
			if first := fmt.Sprint(model.requests[0]["messages"]); !strings.Contains(first, "<file path=") {
				t.Fatal("@main.go context was not attached")
			}
			if last := fmt.Sprint(model.requests[len(model.requests)-1]["messages"]); !strings.Contains(last, "checked") {
				t.Fatal("shell output did not reach the model")
			}
			// One click undoes the turn's file changes.
			h.app.undoAgentTurn(h.app.run())
			if got := string(h.app.bufs.Get(main).Bytes()); strings.Contains(got, "println(1)") {
				t.Fatalf("undo left %q", got)
			}
			// Saved for later.
			if list := h.app.agent.store.List(); len(list) != 1 || !strings.HasPrefix(list[0].Title, "make main print 1") {
				t.Fatalf("sessions %+v", list)
			}
		})
	}
}

func TestRubberDuckPanelCannotEdit(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{
		{{"edit", `{"path":"main.go","old_string":"func main() {}","new_string":"x"}`}},
		nil,
	}}
	h, root := agentProject(t, "openai", model)
	runTask(t, h, "/mode rubber-duck")
	if h.app.run().mode != "rubber-duck" {
		t.Fatalf("mode %q", h.app.run().mode)
	}
	runTask(t, h, "fix main")
	h.until("turn end", 10*time.Second, func() bool { return !h.app.run().running })
	if data, _ := os.ReadFile(filepath.Join(root, "main.go")); strings.Contains(string(data), "x") {
		t.Fatal("rubber-duck edited a file")
	}
	for _, tool := range model.requests[0]["tools"].([]any) {
		name := tool.(map[string]any)["function"].(map[string]any)["name"]
		if name == "edit" || name == "write" || name == "shell" {
			t.Fatalf("rubber-duck offered %v", name)
		}
	}
}

func TestManualEditReviewPerHunk(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{
		{{"write", `{"path":"util/strings.go","content":"package util\n\n// A is new.\nfunc A() {}\n"}`}},
		nil,
	}}
	h, root := agentProject(t, "openai", model)
	h.app.setSetting([]string{"modes", "auto", "approval", "edit"}, "ask")
	runTask(t, h, "add A")
	h.until("edit review", 10*time.Second, func() bool { return h.app.run().approval != nil })
	p := h.app.run().approval
	if len(p.files) != 1 || len(p.files[0].hunks) != 1 {
		t.Fatalf("review %+v", p.files)
	}
	h.frame() // the review card renders
	p.files[0].accepted[0] = false
	h.app.answerApproval(h.app.run(), true, false)
	h.until("turn end", 10*time.Second, func() bool { return !h.app.run().running })
	if data, _ := os.ReadFile(filepath.Join(root, "util", "strings.go")); string(data) != "package util\n" {
		t.Fatalf("rejected hunk was applied: %q", data)
	}
}

func TestModelSettingFromProviderList(t *testing.T) {
	for _, kind := range []string{"anthropic", "openai"} {
		t.Run(kind, func(t *testing.T) {
			model := &fakeModel{turns: [][]toolCall{nil}}
			h, _ := agentProject(t, kind, model)
			h.until("key read", 10*time.Second, func() bool { return h.app.providerReady("test") })
			h.until("model list", 10*time.Second, func() bool {
				m, _, errText := h.app.modelsFor("test")
				if errText != "" {
					t.Fatal(errText)
				}
				return m != nil
			})
			item := h.app.modelSetting("test")
			if len(item.Options) != 2 || item.Options[item.Selected].Value != "test-model" {
				t.Fatalf("options %+v selected %d", item.Options, item.Selected)
			}
			item.OnChange("other-model")
			h.until("config reload", 10*time.Second, func() bool { return h.app.modelOf("test") == "other-model" })
			runTask(t, h, "hi")
			h.until("turn end", 10*time.Second, func() bool { return !h.app.run().running })
			if got := model.requests[0]["model"]; got != "other-model" {
				t.Fatalf("request model %v", got)
			}
		})
	}
}

func TestModelSettingNeedsKey(t *testing.T) {
	root := project(t)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), `
agent:
  providers:
    remote: { type: openai, base_url: "https://example.invalid/v1", api_key: "keychain:remote" }
    local: { type: openai, base_url: "http://localhost:11434/v1" }
`)
	h := newHarness(t, root, false)
	if h.app.providerReady("remote") {
		t.Fatal("a remote provider without a key is ready")
	}
	if !h.app.providerReady("local") {
		t.Fatal("a local provider needs no key")
	}
	h.app.agent.keys.Set("remote", "sk-test")
	h.app.refreshProviderKeys()
	h.until("key read", 10*time.Second, func() bool { return h.app.providerReady("remote") })
}

// Docked as a panel, the composer opens tall at the top of a new session
// and moves to the bottom, shorter, once the chat has a message.
func TestComposerMovesDownAfterFirstMessage(t *testing.T) {
	h := newHarness(t, project(t), false)
	h.app.setSetting([]string{"agent", "layout"}, "panel")
	h.app.agentOpen = true
	h.frame()
	input := func() *ui.TextInput {
		return h.c.Widget(h.app.run().wid("input"), func() any { return nil }).(*ui.TextInput)
	}
	fresh := input().FocusEl().Frame
	if fresh.Y > 200 {
		t.Fatalf("a new session's composer should sit at the top, y=%v", fresh.Y)
	}

	s := h.app.run().session
	s.Messages = append(s.Messages, provider.Message{Role: provider.User,
		Blocks: []provider.Block{{Type: provider.Text, Text: "hello"}}})
	h.frame()
	h.frame()
	chat := input().FocusEl().Frame
	if chat.Y < 600 {
		t.Fatalf("once the chat starts the composer should sit at the bottom, y=%v", chat.Y)
	}
	if chat.H >= fresh.H {
		t.Fatalf("the composer should shrink: %v -> %v", fresh.H, chat.H)
	}
}

func TestOpeningAgentFocusesInput(t *testing.T) {
	h := newHarness(t, project(t), false)
	h.app.toggleAgent()
	h.frame()
	h.frame()
	tf := h.c.Widget(h.app.run().wid("input"), func() any { return nil }).(*ui.TextInput)
	if !tf.Focused() {
		t.Fatal("opening the agent panel should focus the message box")
	}
}

func TestInstructionFilesInSystemPrompt(t *testing.T) {
	model := &fakeModel{}
	h, root := agentProject(t, "anthropic", model)
	agents := filepath.Join(root, "AGENTS.md")
	writeFile(t, agents, "Run make test before you finish.")
	h.app.contextChanged(workspace.Changes{Files: map[string]bool{agents: true}})
	h.frame()
	r := h.app.run()
	if got := r.ctx.Loaded(); len(got) != 1 || got[0].Rel != "AGENTS.md" {
		t.Fatalf("loaded %+v", got)
	}

	system := func(i int) string { return fmt.Sprint(model.requests[i]["system"]) }
	runTask(t, h, "hello")
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	if s := system(0); !strings.Contains(s, "## AGENTS.md\nRun make test before you finish.") {
		t.Fatalf("system prompt lacks AGENTS.md:\n%s", s)
	}

	// A session under way keeps its system prompt, so it stays cached.
	writeFile(t, agents, "Changed.")
	h.app.contextChanged(workspace.Changes{Files: map[string]bool{agents: true}})
	runTask(t, h, "again")
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	if system(1) != system(0) {
		t.Fatalf("system prompt changed mid-session:\n%s", system(1))
	}

	runTask(t, h, "/context")
	if !strings.Contains(r.info, "• AGENTS.md") {
		t.Fatalf("/context said %q", r.info)
	}
}

func TestPromptTemplates(t *testing.T) {
	model := &fakeModel{}
	h, root := agentProject(t, "anthropic", model)
	writeFile(t, filepath.Join(root, "notes.txt"), "Check error handling.")
	writeFile(t, filepath.Join(root, ".hm", "prompts", "review.md"),
		"---\ndescription: Review a file\nargument-hint: <file>\nmode: lazy\n---\nReview $1 for $2.\n@notes.txt\n@../outside.txt\n")
	writeFile(t, filepath.Join(root, ".claude", "commands", "fix.md"),
		"---\nallowed-tools: Bash(go test:*)\ndescription: Fix a bug\n---\nFix this: $ARGUMENTS")
	writeFile(t, filepath.Join(root, ".hm", "prompts", "context.md"), "never sent")
	r := h.app.run()

	sugg, _, _ := h.app.agentSuggest("/", 1)
	found := map[string]ui.Suggestion{}
	for _, s := range sugg {
		found[s.Label] = s
	}
	if s := found["/review <file>"]; s.Detail != "Review a file" || s.Insert != "/review " {
		t.Fatalf("no /review suggestion: %+v", sugg)
	}
	if _, ok := found["/fix"]; !ok {
		t.Fatalf("no /fix suggestion: %+v", sugg)
	}
	if n := len(h.app.agent.shadowWarned); n != 1 {
		t.Errorf("context.md should be warned about once, got %d", n)
	}

	runTask(t, h, `/review main.go "nil checks"`)
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	first := r.session.Messages[0]
	want := "Review main.go for nil checks.\nCheck error handling.\n@../outside.txt"
	if first.TextOf() != want {
		t.Fatalf("expanded to %q, want %q", first.TextOf(), want)
	}
	if r.mode != "lazy" {
		t.Errorf("mode %q, want lazy", r.mode)
	}
	if got := r.templateOf(first); got != "/review" {
		t.Errorf("label %q", got)
	}
	if !strings.Contains(fmt.Sprint(model.requests[0]["messages"]), "Review main.go for nil checks.") {
		t.Fatal("the model did not get the expanded template")
	}

	// A .claude/commands file works as it is.
	runTask(t, h, "/fix the failing test @main.go")
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	if got := fmt.Sprint(model.requests[1]["messages"]); !strings.Contains(got, "Fix this: the failing test @main.go") || !strings.Contains(got, "<file path=") {
		t.Fatalf("second request %s", got)
	}

	// Built-ins win: /context answers and sends nothing.
	runTask(t, h, "/context")
	if r.info == "" || len(model.requests) != 2 {
		t.Fatalf("/context ran the template: info %q, %d requests", r.info, len(model.requests))
	}

	// A new template shows once its folder changes.
	late := filepath.Join(root, ".hm", "prompts", "late.md")
	writeFile(t, late, "late")
	h.app.promptsChanged(workspace.Changes{Files: map[string]bool{late: true}})
	if _, ok := h.app.templateFor("/late"); !ok {
		t.Fatal("/late not found after the change")
	}

	r.input = "/nope"
	h.app.sendAgent()
	if !strings.Contains(r.errText, "Unknown command /nope") {
		t.Fatalf("errText %q", r.errText)
	}
}

func TestPromptTemplateIncludeFollowsGuard(t *testing.T) {
	h, root := agentProject(t, "anthropic", &fakeModel{})
	writeFile(t, filepath.Join(root, ".env"), "SECRET=1")
	writeFile(t, filepath.Join(root, "ok.txt"), "fine")
	writeFile(t, filepath.Join(root, ".hm", "guard.yaml"), "paths:\n  deny: [\".env\"]\n")
	for _, tc := range []struct {
		path string
		ok   bool
	}{
		{"ok.txt", true},
		{".env", false},
		{"../x", false},
		{"/etc/hosts", false},
		{"missing.txt", false},
		{".hm", false},
	} {
		got, err := h.app.promptInclude(tc.path)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %q %v", tc.path, got, err)
		}
	}
}

func TestContextWindow(t *testing.T) {
	root := project(t)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), `
agent:
  providers:
    haiku: { type: anthropic, model: claude-haiku-4-5 }
    local: { type: openai, base_url: "http://localhost:11434/v1", model: llama3.2 }
    sized: { type: openai, base_url: "http://localhost:11434/v1", model: llama3.2, context_window: 32768 }
`)
	h := newHarness(t, root, false)
	for name, want := range map[string]int{"haiku": 200_000, "local": 0, "sized": 32768} {
		if got := h.app.contextWindow(name); got != want {
			t.Errorf("contextWindow(%s) = %d, want %d", name, got, want)
		}
	}
	for n, want := range map[int64]string{950: "950", 1234: "1.2k", 42_000: "42k", 200_000: "200k", 1_000_000: "1M", 1_500_000: "1.5M"} {
		if got := tokens(n); got != want {
			t.Errorf("tokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRateLimitRetriesAndContinues(t *testing.T) {
	for _, kind := range []string{"anthropic", "openai"} {
		t.Run(kind, func(t *testing.T) {
			model := &fakeModel{limited: 1}
			h, _ := agentProject(t, kind, model)
			start := time.Now()
			runTask(t, h, "hello")
			h.until("retry notice", 5*time.Second, func() bool {
				return strings.HasPrefix(h.app.run().retry, "Rate limited, retrying in 1s (1/5)")
			})
			h.until("turn end", 10*time.Second, func() bool { return !h.app.run().running })
			if d := time.Since(start); d < 800*time.Millisecond {
				t.Fatalf("finished after %v; Retry-After was 1s", d)
			}
			r := h.app.run()
			if r.errText != "" || r.retry != "" {
				t.Fatalf("error %q, retry %q", r.errText, r.retry)
			}
			s := r.session
			if last := s.Messages[len(s.Messages)-1]; last.TextOf() != "Done: main prints 1." {
				t.Fatalf("last message %+v", last)
			}
		})
	}
}
