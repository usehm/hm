package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fixture serves a recorded SSE stream and keeps every request body.
func fixture(t *testing.T, file string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["_path"] = r.URL.Path
		m["_auth"] = r.Header.Get("Authorization") + r.Header.Get("X-Api-Key")
		m["_beta"] = r.Header.Get("Anthropic-Beta")
		bodies = append(bodies, m)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

var readTool = Tool{Name: "read", Description: "Read a file",
	Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)}

func checkToolTurn(t *testing.T, resp Response, events []Event) {
	t.Helper()
	if resp.StopReason != StopToolUse {
		t.Fatalf("stop %v", resp.StopReason)
	}
	if got := resp.Message.TextOf(); got != "Reading the file." {
		t.Fatalf("text %q", got)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 1 || uses[0].ToolName != "read" || string(uses[0].Input) != `{"path":"a.go"}` {
		t.Fatalf("tool uses %+v", uses)
	}
	var text strings.Builder
	for _, e := range events {
		if e.Kind == EventText {
			text.WriteString(e.Text)
		}
	}
	if text.String() != "Reading the file." {
		t.Fatalf("streamed %q", text.String())
	}
}

func TestAnthropicStreamAndReplay(t *testing.T) {
	srv, bodies := fixture(t, "anthropic_tool_use.sse")
	p := NewAnthropic(Config{APIKey: "sk-test", BaseURL: srv.URL})
	req := Request{Model: "claude-opus-5-5", System: "You are hm.", Tools: []Tool{readTool}, Effort: "high",
		Messages: []Message{{Role: User, Blocks: []Block{{Type: Text, Text: "read a.go"}}}}}
	var events []Event
	resp, err := p.Stream(context.Background(), req, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	checkToolTurn(t, resp, events)
	if resp.Message.Blocks[0].Type != Thinking || resp.Message.Blocks[0].Text != "Need to read a.go." {
		t.Fatalf("thinking %+v", resp.Message.Blocks[0])
	}
	u := resp.Usage
	if u.Input != 120 || u.Output != 45 || u.CacheRead != 2000 || u.CacheWrite != 300 {
		t.Fatalf("usage %+v", u)
	}
	if want := (120*4 + 45*20 + 2000*0.2 + 300*5) / 1e6; u.Cost != want {
		t.Fatalf("cost %v want %v", u.Cost, want)
	}

	b := (*bodies)[0]
	if b["_path"] != "/v1/messages" || b["model"] != "claude-opus-5-5" {
		t.Fatalf("request %v %v", b["_path"], b["model"])
	}
	if th, _ := b["thinking"].(map[string]any); th["type"] != "adaptive" {
		t.Fatalf("thinking %v", b["thinking"])
	}
	if oc, _ := b["output_config"].(map[string]any); oc["effort"] != "high" {
		t.Fatalf("effort %v", b["output_config"])
	}
	if b["cache_control"] == nil {
		t.Fatal("prompt caching off")
	}
	tools := b["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["eager_input_streaming"] != true || tool["input_schema"].(map[string]any)["additionalProperties"] != false {
		t.Fatalf("tool %v", tool)
	}
	if _, ok := b["fallbacks"]; ok {
		t.Fatal("no server-side fallback through a custom base URL")
	}

	// Next turn: the assistant message goes back exactly, thinking and its
	// signature included.
	req.Messages = append(req.Messages, resp.Message, Message{Role: User, Blocks: []Block{
		{Type: ToolResult, ToolID: "toolu_1", Text: "package a"}}})
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	msgs := (*bodies)[1]["messages"].([]any)
	asst := msgs[1].(map[string]any)["content"].([]any)
	first := asst[0].(map[string]any)
	if first["type"] != "thinking" || first["signature"] != "sig-abc" {
		t.Fatalf("replayed assistant %v", asst)
	}
	res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if res["type"] != "tool_result" || res["tool_use_id"] != "toolu_1" {
		t.Fatalf("tool result %v", res)
	}
}

func TestAnthropicFallbackOnFirstParty(t *testing.T) {
	a := NewAnthropic(Config{APIKey: "x"})
	p, err := a.params(Request{Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(p)
	if !strings.Contains(string(data), `"fallbacks":"default"`) {
		t.Fatalf("fallbacks missing: %s", data)
	}
	if len(p.Betas) != 1 || p.Betas[0] != "server-side-fallback-2026-07-01" {
		t.Fatalf("betas %v", p.Betas)
	}
	// Another model's history drops thinking instead of replaying it.
	m := Message{Role: Assistant, NativeFrom: "anthropic:claude-sonnet-5-5", Native: json.RawMessage(`{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"s"}]}`),
		Blocks: []Block{{Type: Thinking, Text: "x"}, {Type: Text, Text: "hi"}}}
	mp, _ := toAnthropic(m, a.Name("claude-opus-5-5"))
	out, _ := json.Marshal(mp)
	if strings.Contains(string(out), "thinking") || !strings.Contains(string(out), "hi") {
		t.Fatalf("converted %s", out)
	}
}

func TestOpenRouterStream(t *testing.T) {
	srv, bodies := fixture(t, "openrouter_tool_use.sse")
	p := NewOpenAI(Config{APIKey: "or-key", BaseURL: srv.URL + "/api/v1"})
	req := Request{Model: "anthropic/claude-sonnet-5-5", System: "You are hm.", Tools: []Tool{readTool},
		Messages: []Message{{Role: User, Blocks: []Block{{Type: Text, Text: "read a.go"}}}}}
	var events []Event
	resp, err := p.Stream(context.Background(), req, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	checkToolTurn(t, resp, events)
	if u := resp.Usage; u.Input != 300 || u.CacheRead != 200 || u.Output != 30 || u.Cost != 0.0012 {
		t.Fatalf("usage %+v", u)
	}
	b := (*bodies)[0]
	if b["_path"] != "/api/v1/chat/completions" || b["_auth"] != "Bearer or-key" || b["stream"] != true {
		t.Fatalf("request %v", b)
	}
	msgs := b["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("messages %v", msgs)
	}

	// Tool results become role "tool" messages in the next request.
	req.Messages = append(req.Messages, resp.Message, Message{Role: User, Blocks: []Block{
		{Type: ToolResult, ToolID: "call_9", Text: "package a"}}})
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	msgs = (*bodies)[1]["messages"].([]any)
	asst := msgs[2].(map[string]any)
	tc := asst["tool_calls"].([]any)[0].(map[string]any)
	if tc["id"] != "call_9" || tc["function"].(map[string]any)["arguments"] != `{"path":"a.go"}` {
		t.Fatalf("assistant %v", asst)
	}
	if tool := msgs[3].(map[string]any); tool["role"] != "tool" || tool["tool_call_id"] != "call_9" {
		t.Fatalf("tool message %v", tool)
	}
}

func TestOpenAIErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()
	_, err := NewOpenAI(Config{BaseURL: srv.URL}).Stream(context.Background(), Request{Model: "m"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatal(err)
	}
}
