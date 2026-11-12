// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

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

func TestAzure(t *testing.T) {
	data, err := os.ReadFile("testdata/openrouter_tool_use.sse")
	if err != nil {
		t.Fatal(err)
	}
	type seen struct{ uri, key, bearer, model string }
	var reqs []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		model, _ := body["model"].(string)
		reqs = append(reqs, seen{r.URL.RequestURI(), r.Header.Get("Api-Key"), r.Header.Get("Authorization"), model})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	req := Request{Model: "my-deploy", Tools: []Tool{readTool},
		Messages: []Message{{Role: User, Blocks: []Block{{Type: Text, Text: "read a.go"}}}}}
	cases := []struct{ name, base, version, uri string }{
		{"deployment", srv.URL, "2024-10-21", "/openai/deployments/my-deploy/chat/completions?api-version=2024-10-21"},
		{"v1", srv.URL + "/openai/v1/", "", "/openai/v1/chat/completions"},
	}
	for i, tc := range cases {
		p, err := New(Config{Type: "azure", BaseURL: tc.base, APIKey: "az-key", APIVersion: tc.version})
		if err != nil {
			t.Fatal(err)
		}
		var events []Event
		resp, err := p.Stream(context.Background(), req, func(e Event) { events = append(events, e) })
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		checkToolTurn(t, resp, events)
		if r := reqs[i]; r.uri != tc.uri || r.key != "az-key" || r.bearer != "" || r.model != "my-deploy" {
			t.Errorf("%s: request %+v", tc.name, r)
		}
		if !strings.HasPrefix(p.Name("m"), "azure:") {
			t.Errorf("%s: name %s", tc.name, p.Name("m"))
		}
		if _, err := p.Models(context.Background()); err == nil || !strings.Contains(err.Error(), "deployment name") {
			t.Errorf("%s: models %v", tc.name, err)
		}
	}
	if _, err := NewAzure(Config{APIKey: "k"}).Stream(context.Background(), req, nil); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("no base URL: %v", err)
	}
}

func TestOpenAIReasoning(t *testing.T) {
	cases := []struct {
		name, base, model, effort string
		max                       int
		has, lacks                []string
	}{
		{"gpt-5 off", "https://api.openai.com/v1", "gpt-5", "off", 0, []string{`"reasoning_effort":"minimal"`}, nil},
		{"o3 off", "https://api.openai.com/v1", "o3", "off", 0, []string{`"reasoning_effort":"low"`}, nil},
		{"o4-mini max", "https://api.openai.com/v1", "o4-mini", "max", 4096,
			[]string{`"reasoning_effort":"high"`, `"max_completion_tokens":4096`}, []string{`"max_tokens"`}},
		{"no effort", "https://api.openai.com/v1", "gpt-5.1", "", 0, nil, []string{"reasoning_effort"}},
		{"gpt-4o", "https://api.openai.com/v1", "gpt-4o", "high", 100, []string{`"max_tokens":100`}, []string{"reasoning_effort"}},
		{"another server", "https://api.groq.com/openai/v1", "gpt-5", "high", 0, nil, []string{"reasoning_effort"}},
	}
	for _, tc := range cases {
		data, err := NewOpenAI(Config{BaseURL: tc.base}).body(Request{Model: tc.model, Effort: tc.effort, MaxTokens: tc.max})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range tc.has {
			if !strings.Contains(string(data), w) {
				t.Errorf("%s: lacks %s in %s", tc.name, w, data)
			}
		}
		for _, w := range tc.lacks {
			if strings.Contains(string(data), w) {
				t.Errorf("%s: has %s in %s", tc.name, w, data)
			}
		}
	}
	data, _ := NewAzure(Config{BaseURL: "https://r.openai.azure.com"}).body(Request{Model: "gpt-5-mini", Effort: "medium"})
	if !strings.Contains(string(data), `"reasoning_effort":"medium"`) {
		t.Errorf("azure gpt-5: %s", data)
	}
}
