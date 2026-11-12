// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// googleServer serves a recorded stream, or status and body when status is
// not 200, and keeps each request's URL, key and body.
type googleRequest struct {
	url, key, auth string
	body           map[string]any
}

func googleServer(t *testing.T, status int, file string) (*httptest.Server, *[]googleRequest) {
	t.Helper()
	data, err := os.ReadFile("testdata/" + file)
	if err != nil {
		data = []byte(file) // a body given inline
	}
	var reqs []googleRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		reqs = append(reqs, googleRequest{r.URL.RequestURI(), r.Header.Get("X-Goog-Api-Key"), r.Header.Get("Authorization"), m})
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
		} else {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func TestGoogleStreamAndReplay(t *testing.T) {
	srv, reqs := googleServer(t, 200, "gemini_tool_use.sse")
	p := WithPricing(NewGoogle(Config{APIKey: "g-key", BaseURL: srv.URL}), nil, "google")
	req := Request{Model: "gemini-2.5-pro", System: "You are hm.", Tools: []Tool{readTool}, Effort: "high", MaxTokens: 1000,
		Messages: []Message{{Role: User, Blocks: []Block{{Type: Text, Text: "read a.go"}}}}}
	var events []Event
	resp, err := p.Stream(context.Background(), req, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	checkToolTurn(t, resp, events)
	if b := resp.Message.Blocks[0]; b.Type != Thinking || b.Text != "Need to read a.go." {
		t.Fatalf("thinking %+v", b)
	}
	if u := resp.Usage; u.Input != 100 || u.CacheRead != 200 || u.Output != 30 || resp.Model != "gemini-2.5-pro" {
		t.Fatalf("usage %+v model %s", u, resp.Model)
	}
	if id := resp.Message.ToolUses()[0].ToolID; id != "gcall_0" {
		t.Fatalf("tool id %s", id)
	}

	r := (*reqs)[0]
	if r.url != "/models/gemini-2.5-pro:streamGenerateContent?alt=sse" || r.key != "g-key" {
		t.Fatalf("request %s key %q", r.url, r.key)
	}
	b, _ := json.Marshal(r.body)
	for _, want := range []string{
		`"systemInstruction":{"parts":[{"text":"You are hm."}]`,
		`"thinkingConfig":{"includeThoughts":true,"thinkingBudget":24576}`,
		`"maxOutputTokens":1000`,
		`"functionDeclarations":[{"description":"Read a file","name":"read","parameters":{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"}}]`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("request lacks %s:\n%s", want, b)
		}
	}

	// Next turn: Gemini's own parts go back with their signature; the
	// result names its call, and the made-up id is not sent.
	req.Messages = append(req.Messages, resp.Message, Message{Role: User, Blocks: []Block{
		{Type: ToolResult, ToolID: "gcall_0", Text: "package a"}}})
	if _, err := p.Stream(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal((*reqs)[1].body["contents"])
	for _, want := range []string{
		`{"parts":[{"text":"Need to read a.go.","thought":true},{"text":"Reading the file."},{"functionCall":{"args":{"path":"a.go"},"name":"read"},"thoughtSignature":"sig-g"}],"role":"model"}`,
		`{"parts":[{"functionResponse":{"name":"read","response":{"result":"package a"}}}],"role":"user"}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("replay lacks %s:\n%s", want, b)
		}
	}
}

func TestGoogleOtherProvidersHistory(t *testing.T) {
	g := NewGoogle(Config{APIKey: "k"})
	data, err := g.body(Request{Model: "gemini-3-pro", Messages: []Message{
		{Role: User, Blocks: []Block{{Type: Text, Text: "go"}}},
		{Role: Assistant, NativeFrom: "anthropic:claude-opus-5-5", Native: json.RawMessage(`{"content":[]}`), Blocks: []Block{
			{Type: Thinking, Text: "secret"}, {Type: Text, Text: "Reading."},
			{Type: ToolUse, ToolID: "toolu_1", ToolName: "read", Input: json.RawMessage(`{"path":"a.go"}`)}}},
		{Role: User, Blocks: []Block{{Type: ToolResult, ToolID: "toolu_1", Text: "no such file", IsError: true}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "secret") {
		t.Fatalf("another provider's thinking was sent: %s", s)
	}
	for _, want := range []string{
		`{"role":"model","parts":[{"text":"Reading."},{"functionCall":{"id":"toolu_1","name":"read","args":{"path":"a.go"}}}]}`,
		`{"functionResponse":{"id":"toolu_1","name":"read","response":{"error":"no such file"}}}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("body lacks %s:\n%s", want, s)
		}
	}
}

func TestGoogleThinkingOnly(t *testing.T) {
	srv, _ := googleServer(t, 200, "gemini_thinking.sse")
	var thinking strings.Builder
	resp, err := NewGoogle(Config{APIKey: "k", BaseURL: srv.URL}).Stream(context.Background(),
		Request{Model: "gemini-3-flash"}, func(e Event) {
			if e.Kind == EventThinking {
				thinking.WriteString(e.Text)
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != StopEnd || resp.Message.TextOf() != "The answer is 4." || thinking.String() != "Let me think about it." {
		t.Fatalf("resp %+v thinking %q", resp, thinking.String())
	}
	if !strings.Contains(string(resp.Message.Native), `{"text":"The answer is 4.","thoughtSignature":"sig-end"}`) {
		t.Fatalf("native %s", resp.Message.Native)
	}
	if resp.Usage.Output != 18 {
		t.Fatalf("usage %+v", resp.Usage)
	}
}

func TestGoogleErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   error
	}{
		{"in the stream", 200, "gemini_error.sse", ErrRateLimited},
		{"quota", 429, `{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`, ErrRateLimited},
		{"overloaded", 503, `{"error":{"code":503,"message":"The model is overloaded.","status":"UNAVAILABLE"}}`, ErrOverloaded},
		{"bad key", 400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`, ErrAuth},
		{"too long", 400, `{"error":{"code":400,"message":"The input token count (1200000) exceeds the maximum number of tokens allowed (1048576).","status":"INVALID_ARGUMENT"}}`, ErrContextOverflow},
		{"listed", 500, `[{"error":{"code":500,"message":"Internal error","status":"INTERNAL"}}]`, nil},
	}
	for _, tc := range cases {
		srv, _ := googleServer(t, tc.status, tc.body)
		_, err := NewGoogle(Config{APIKey: "k", BaseURL: srv.URL}).Stream(context.Background(), Request{Model: "gemini-2.5-flash"}, nil)
		var ae *APIError
		if !errors.As(err, &ae) || ae.Kind != tc.kind {
			t.Errorf("%s: %v (kind %v)", tc.name, err, ae)
			continue
		}
		if tc.name == "listed" && (ae.Status != 500 || !strings.Contains(ae.Msg, "Internal error")) {
			t.Errorf("listed: %+v", ae)
		}
	}
	if _, err := NewGoogle(Config{}).Stream(context.Background(), Request{Model: "m"}, nil); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key: %v", err)
	}
}

func TestGeminiThinking(t *testing.T) {
	cases := []struct{ model, effort, want string }{
		{"gemini-2.5-pro", "", `{"includeThoughts":true}`},
		{"gemini-2.5-pro", "low", `{"includeThoughts":true,"thinkingBudget":1024}`},
		{"gemini-2.5-pro", "off", `{"includeThoughts":true,"thinkingBudget":128}`},
		{"gemini-2.5-flash", "off", `{"thinkingBudget":0}`},
		{"gemini-2.5-flash", "max", `{"includeThoughts":true,"thinkingBudget":24576}`},
		{"gemini-3-pro-preview", "medium", `{"includeThoughts":true,"thinkingLevel":"high"}`},
		{"gemini-3-flash", "off", `{"includeThoughts":true,"thinkingLevel":"minimal"}`},
		{"models/gemini-3-pro", "low", `{"includeThoughts":true,"thinkingLevel":"low"}`},
		{"gemini-2.0-flash", "high", `null`},
		{"gemma-3-27b-it", "high", `null`},
	}
	for _, tc := range cases {
		got, _ := json.Marshal(geminiThinking(tc.model, tc.effort))
		if string(got) != tc.want {
			t.Errorf("%s %s: %s, want %s", tc.model, tc.effort, got, tc.want)
		}
	}
}

func TestGoogleVertex(t *testing.T) {
	srv, reqs := googleServer(t, 200, "gemini_thinking.sse")
	// A key on Vertex AI is an access token; without one hm asks Google
	// Cloud's default credentials.
	p, err := New(Config{Type: "google", Project: "my-proj", BaseURL: srv.URL, APIKey: "ya29.token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(context.Background(), Request{Model: "gemini-2.5-pro"}, nil); err != nil {
		t.Fatal(err)
	}
	r := (*reqs)[0]
	if r.url != "/v1/projects/my-proj/locations/global/publishers/google/models/gemini-2.5-pro:streamGenerateContent?alt=sse" ||
		r.auth != "Bearer ya29.token" || r.key != "" {
		t.Fatalf("request %s auth %q key %q", r.url, r.auth, r.key)
	}
	for loc, want := range map[string]string{
		"":            "https://aiplatform.googleapis.com/v1/projects/p/locations/global/publishers/google",
		"us-central1": "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/publishers/google",
	} {
		if g := NewGoogle(Config{Project: "p", Location: loc}); g.base != want {
			t.Errorf("location %q: %s", loc, g.base)
		}
	}
	if _, err := p.Models(context.Background()); err == nil {
		t.Error("Vertex AI listed models")
	}
}

func TestGoogleModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-Api-Key") != "k" || r.URL.Path != "/models" {
			http.Error(w, `{"error":{"code":403,"message":"no","status":"PERMISSION_DENIED"}}`, 403)
			return
		}
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-2.5-pro","supportedGenerationMethods":["generateContent","countTokens"]},
				{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]}],"nextPageToken":"p2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent"]}]}`)
	}))
	defer srv.Close()
	got, err := NewGoogle(Config{APIKey: "k", BaseURL: srv.URL}).Models(context.Background())
	if err != nil || strings.Join(got, " ") != "gemini-2.5-flash gemini-2.5-pro" {
		t.Fatalf("models %v %v", got, err)
	}
	if _, err := NewGoogle(Config{APIKey: "bad", BaseURL: srv.URL}).Models(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("bad key: %v", err)
	}
}
