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
	h.app.agentOpen = true
	h.frame()
	return h, root
}

func runTask(t *testing.T, h *harness, text string) {
	t.Helper()
	h.app.agent.input = text
	h.app.sendAgent()
	if h.app.agent.errText != "" {
		t.Fatal(h.app.agent.errText)
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
			h.until("shell approval", 10*time.Second, func() bool { return h.app.agent.approval != nil })
			p := h.app.agent.approval
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
			h.app.answerApproval(true, false)
			h.until("turn end", 10*time.Second, func() bool { return !h.app.agent.running })
			s := h.app.agent.session
			if last := s.Messages[len(s.Messages)-1]; last.TextOf() != "Done: main prints 1." {
				t.Fatalf("last message %+v", last)
			}
			if u := s.UsageNow(); u.Input == 0 || u.Output == 0 {
				t.Fatalf("usage %+v", s.Usage)
			}
			// The model saw the shell output, and the @main.go context.
			if first := fmt.Sprint(model.requests[0]["messages"]); !strings.Contains(first, "<file path=") {
				t.Fatal("@main.go context was not attached")
			}
			if last := fmt.Sprint(model.requests[len(model.requests)-1]["messages"]); !strings.Contains(last, "checked") {
				t.Fatal("shell output did not reach the model")
			}
			// One click undoes the turn's file changes.
			h.app.undoAgentTurn()
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
	if h.app.agent.mode != "rubber-duck" {
		t.Fatalf("mode %q", h.app.agent.mode)
	}
	runTask(t, h, "fix main")
	h.until("turn end", 10*time.Second, func() bool { return !h.app.agent.running })
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
	h.until("edit review", 10*time.Second, func() bool { return h.app.agent.approval != nil })
	p := h.app.agent.approval
	if len(p.files) != 1 || len(p.files[0].hunks) != 1 {
		t.Fatalf("review %+v", p.files)
	}
	h.frame() // the review card renders
	p.files[0].accepted[0] = false
	h.app.answerApproval(true, false)
	h.until("turn end", 10*time.Second, func() bool { return !h.app.agent.running })
	if data, _ := os.ReadFile(filepath.Join(root, "util", "strings.go")); string(data) != "package util\n" {
		t.Fatalf("rejected hunk was applied: %q", data)
	}
}
