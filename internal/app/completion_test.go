// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
)

// completionProject opens main.go against a fake OpenAI-compatible endpoint
// that answers every completion with reply, and counts the requests.
func completionProject(t *testing.T, reply string) (*harness, *int, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		n++
		mu.Unlock()
		if body.Model != "fast-model" || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "<CURSOR>") {
			http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": reply}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(srv.Close)
	root := project(t)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), fmt.Sprintf(`
agent:
  default_provider: test
  providers:
    test: { type: openai, base_url: %q, api_key: "keychain:test", model: big-model }
completion:
  model: fast-model
  delay: 10ms
`, srv.URL+"/v1"))
	h := newHarness(t, root, false)
	h.app.agent.keys.Set("test", "sk-test")
	h.app.refreshProviderKeys()
	h.until("key read", 10*time.Second, func() bool { return h.app.completionReady() })
	h.app.openFile(filepath.Join(root, "main.go"))
	h.frame()
	return h, &n, &mu
}

func TestCompletionShowsGhostTextAndTabAccepts(t *testing.T) {
	h, _, _ := completionProject(t, "println(1)")
	ed := h.app.ws.ActiveEditor()
	at := strings.Index(string(ed.Bytes()), "{}") + 1
	ed.SetSelection(at, at)
	h.kb.TypeRune(' ')
	h.frame()
	h.until("ghost text", 10*time.Second, func() bool { return ed.InlineSuggestion() != "" })
	if got := ed.InlineSuggestion(); got != "println(1)" {
		t.Fatalf("suggestion %q", got)
	}
	h.press(input.KeyTab, 0)
	if got := string(ed.Bytes()); got != "package main\n\nfunc main() { println(1)}\n" {
		t.Fatalf("after Tab: %q", got)
	}
}

func TestCompletionWaitsForEditsNotCaretMoves(t *testing.T) {
	h, n, mu := completionProject(t, "x")
	ed := h.app.ws.ActiveEditor()
	ed.SetSelection(3, 3) // inside "package": not a place to complete
	h.kb.TypeRune('a')
	h.frame()
	ed.SetSelection(len(ed.Bytes()), len(ed.Bytes()))
	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Millisecond)
		h.frame()
	}
	mu.Lock()
	defer mu.Unlock()
	if *n != 0 {
		t.Fatalf("%d requests: an edit mid-word or a caret move should not ask", *n)
	}
}

func TestCompletionTriggerByHand(t *testing.T) {
	h, _, _ := completionProject(t, "// note")
	ed := h.app.ws.ActiveEditor()
	ed.SetSelection(3, 3) // mid-word: only a manual trigger asks here
	h.frame()
	h.press(input.KeyBackslash, input.ModAlt)
	h.until("ghost text", 10*time.Second, func() bool { return ed.InlineSuggestion() == "// note" })
}
