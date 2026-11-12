// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Package agenttest has a scripted fake model and a workspace using it,
// for tests of the agent's hosts.
package agenttest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/usehm/hm/internal/config"
)

// Call is a tool call the fake model makes.
type Call struct{ Name, Input string }

// Reply is one scripted model response: tool calls, or else text.
type Reply struct {
	Calls []Call
	Text  string
}

// Model answers Anthropic streaming requests from a script; once the
// script runs out it answers "done".
type Model struct {
	mu       sync.Mutex
	script   []Reply
	requests []string // each request's body
}

func (f *Model) next(body string) Reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, body)
	if len(f.script) == 0 {
		return Reply{Text: "done"}
	}
	r := f.script[0]
	f.script = f.script[1:]
	return r
}

// Sent reports whether any request so far contains s.
func (f *Model) Sent(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.requests {
		if strings.Contains(b, s) {
			return true
		}
	}
	return false
}

// Reset replaces what is left of the script.
func (f *Model) Reset(script []Reply) {
	f.mu.Lock()
	f.script = script
	f.mu.Unlock()
}

// Last is the latest request's body.
func (f *Model) Last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return ""
	}
	return f.requests[len(f.requests)-1]
}

func sse(w http.ResponseWriter, event string, v any) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func (f *Model) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rep := f.next(string(body))
	w.Header().Set("Content-Type", "text/event-stream")
	sse(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg", "type": "message", "role": "assistant", "model": "test-model", "content": []any{},
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 1}}})
	if len(rep.Calls) == 0 {
		sse(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		sse(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": rep.Text}})
		sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	}
	for i, c := range rep.Calls {
		sse(w, "content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{
			"type": "tool_use", "id": fmt.Sprintf("toolu_%d_%d", len(f.requests), i), "name": c.Name, "input": map[string]any{}}})
		sse(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": c.Input}})
		sse(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	stop := "end_turn"
	if len(rep.Calls) > 0 {
		stop = "tool_use"
	}
	sse(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 20}})
	sse(w, "message_stop", map[string]any{"type": "message_stop"})
}

// Workspace makes a project using a fake model with script, and points
// hm's config and state folders into the test's temp folder.
func Workspace(t *testing.T, script []Reply, extraConfig string) (string, *Model) {
	t.Helper()
	model := &Model{script: script}
	srv := httptest.NewServer(model)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	oldUser, oldState, oldKeys := config.UserDir, config.StateDir, config.Keychain
	config.UserDir = func() string { return filepath.Join(dir, "config") }
	config.StateDir = func() string { return filepath.Join(dir, "state") }
	config.Keychain = func(string) (string, error) { return "sk-test", nil }
	t.Cleanup(func() { config.UserDir, config.StateDir, config.Keychain = oldUser, oldState, oldKeys })
	root := filepath.Join(dir, "project")
	WriteFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	WriteFile(t, filepath.Join(root, ".hm", "config.yaml"), fmt.Sprintf(`
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

// WriteFile writes s to path, making its folder.
func WriteFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
