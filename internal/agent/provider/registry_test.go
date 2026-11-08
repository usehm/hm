package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinLookup(t *testing.T) {
	cases := []struct {
		model  string
		window int
		ok     bool
	}{
		{"claude-opus-5-5", 1_000_000, true},
		{"claude-haiku-4-5-20251001", 200_000, true},
		{"anthropic.claude-sonnet-4-6", 1_000_000, true},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", 200_000, true},
		{"anthropic/claude-opus-5", 1_000_000, true},
		{"openai/gpt-4o-mini", 128_000, true},
		{"gpt-4.1-nano", 1_047_576, true},
		{"llama3.2", 0, false},
	}
	for _, tc := range cases {
		m, ok := Builtin().Lookup("", tc.model)
		if m.ContextWindow != tc.window || ok != tc.ok || m.ID != tc.model {
			t.Errorf("Lookup(%q) = %+v %v", tc.model, m, ok)
		}
	}
	// The longest prefix prices a dated id, not a shorter family's.
	if m, _ := Builtin().Lookup("", "claude-opus-5-5-20261101"); m.Price.Input != 4 {
		t.Errorf("dated opus 5.5 price %+v", m.Price)
	}
}

func writeModels(t *testing.T, json string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(f, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRegistryMerge(t *testing.T) {
	user := writeModels(t, `[
		{"id": "claude-opus-5-5", "context_window": 200000},
		{"id": "llama3.3:70b", "provider": "ollama", "name": "Llama 3.3 70B", "context_window": 131072,
		 "supports": {"tools": true, "images": false}, "efforts": ["low", "high"]},
		{"id": "qwen3", "context_window": 32768, "price": {"input": 0.1, "output": 0.2}}
	]`)
	project := writeModels(t, `[
		{"id": "llama3.3:70b", "provider": "ollama", "context_window": 65536},
		{"id": "qwen3", "provider": "groq", "price": {"input": 1, "output": 2}}
	]`)
	reg, err := Builtin().Merge(user, project, filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	yes := func(b *bool) bool { return b != nil && *b }
	cases := []struct {
		name, prov, model string
		check             func(ModelInfo) bool
	}{
		{"override keeps other fields", "anthropic", "claude-opus-5-5", func(m ModelInfo) bool {
			return m.ContextWindow == 200_000 && m.MaxOutput == 128_000 && m.Price.Input == 4
		}},
		{"later file wins, fields kept", "ollama", "llama3.3:70b", func(m ModelInfo) bool {
			return m.ContextWindow == 65536 && m.Name == "Llama 3.3 70B" && yes(m.Supports.Tools) &&
				m.Supports.Images != nil && !*m.Supports.Images && len(m.Efforts) == 2
		}},
		{"scoped entry is only its provider's", "openai", "llama3.3:70b", func(m ModelInfo) bool {
			return m.ContextWindow == 0
		}},
		{"scoped beats global on its provider", "groq", "qwen3", func(m ModelInfo) bool {
			return m.Price.Input == 1 && m.ContextWindow == 32768
		}},
		{"global applies elsewhere", "together", "qwen3-coder", func(m ModelInfo) bool {
			return m.Price.Input == 0.1 && m.ContextWindow == 32768
		}},
	}
	for _, tc := range cases {
		m, _ := reg.Lookup(tc.prov, tc.model)
		if !tc.check(m) {
			t.Errorf("%s: %+v", tc.name, m)
		}
	}
	// Merging does not change hm's own table.
	if m, _ := Builtin().Lookup("", "claude-opus-5-5"); m.ContextWindow != 1_000_000 {
		t.Errorf("builtin changed: %+v", m)
	}
	var ids []string
	for _, m := range reg.Listed("ollama") {
		ids = append(ids, m.ID)
	}
	if got := strings.Join(ids, " "); got != "llama3.3:70b" {
		t.Errorf("listed for ollama: %s", got)
	}
}

func TestRegistryMergeBadFile(t *testing.T) {
	good := writeModels(t, `[{"id": "a", "context_window": 1000}]`)
	cases := map[string]string{
		"not json": `{"id": `,
		"no id":    `[{"context_window": 5}]`,
	}
	for name, body := range cases {
		reg, err := Builtin().Merge(writeModels(t, body), good)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if m, _ := reg.Lookup("", "a"); m.ContextWindow != 1000 {
			t.Errorf("%s: the good file was not applied: %+v", name, m)
		}
	}
}

// priced is a provider that answers with fixed usage.
type priced struct {
	Provider
	resp Response
}

func (p priced) Stream(context.Context, Request, func(Event)) (Response, error) { return p.resp, nil }

func TestWithPricing(t *testing.T) {
	reg, err := Builtin().Merge(writeModels(t, `[{"id": "local", "provider": "ollama", "price": {"input": 1, "output": 2}}]`))
	if err != nil {
		t.Fatal(err)
	}
	u := Usage{Input: 1_000_000, Output: 1_000_000}
	cases := []struct {
		name, entry, model string
		resp               Response
		want               float64
	}{
		{"priced from the registry", "ollama", "local", Response{Usage: u}, 3},
		{"scoped to its provider", "groq", "local", Response{Usage: u}, 0},
		{"the API's own cost stays", "ollama", "local", Response{Usage: Usage{Input: 1, Cost: 0.5}}, 0.5},
		{"the answering model's price", "anthropic", "claude-opus-5-5", Response{Usage: u, Model: "claude-haiku-4-5"}, 6},
		{"unknown model", "openai", "mystery", Response{Usage: u}, 0},
	}
	for _, tc := range cases {
		p := WithPricing(priced{resp: tc.resp}, reg, tc.entry)
		resp, _ := p.Stream(context.Background(), Request{Model: tc.model}, nil)
		if resp.Usage.Cost != tc.want {
			t.Errorf("%s: cost %v want %v", tc.name, resp.Usage.Cost, tc.want)
		}
	}
}

func TestPresets(t *testing.T) {
	srv, bodies := fixture(t, "openrouter_tool_use.sse")
	cases := []struct{ typ, base, want string }{
		{"groq", "", "https://api.groq.com/openai/v1"},
		{"ollama", "", "http://localhost:11434/v1"},
		{"openai", "", "https://api.openai.com/v1"},
		{"groq", srv.URL, srv.URL},
		{"anthropic", "", ""},
	}
	for _, tc := range cases {
		if got := BaseURL(tc.typ, tc.base); got != tc.want {
			t.Errorf("BaseURL(%q, %q) = %q", tc.typ, tc.base, got)
		}
	}
	p, err := New(Config{Type: "deepseek", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(context.Background(), Request{Model: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	if b := (*bodies)[0]; b["_path"] != "/chat/completions" || b["_auth"] != "Bearer k" {
		t.Errorf("preset request %v %v", b["_path"], b["_auth"])
	}
	if _, err := New(Config{Type: "nope"}); err == nil {
		t.Error("unknown type accepted")
	}
}
