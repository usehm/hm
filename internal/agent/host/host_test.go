package host

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
	"github.com/mirzakhany/hm/internal/config"
)

// testHost is a host over a temp workspace with extra config on top of
// the defaults; warnings go to the returned slice.
func testHost(t *testing.T, yaml string) (*Host, *[]string) {
	t.Helper()
	dir := t.TempDir()
	old := config.UserDir
	config.UserDir = func() string { return filepath.Join(dir, "config") }
	t.Cleanup(func() { config.UserDir = old })
	cfg, ps := config.Load(config.Layer{Name: "test", Data: []byte(yaml)})
	if len(ps) > 0 {
		t.Fatalf("config: %v", ps)
	}
	var warns []string
	root := filepath.Join(dir, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Host{Root: root, Config: cfg, Warn: func(m string) { warns = append(warns, m) }}, &warns
}

// noAgents stands in for the other agents.
type noAgents struct{ tools.Agents }

func TestToolset(t *testing.T) {
	h, warns := testHost(t, `
tools:
  commands:
    read: { description: clash, command: "echo" }
    lint: { description: lint it, command: "echo lint", approval: ask }
`)
	auto := h.Modes()["auto"]
	cases := []struct {
		name       string
		opts       ToolsetOptions
		has, lacks []string
	}{
		{"top agent", ToolsetOptions{Mode: auto, Agents: noAgents{}, Spawn: true},
			[]string{"read", "edit", "shell", "lint", "task", "send_message"}, []string{"lsp_definition"}},
		{"no spawn", ToolsetOptions{Mode: auto, Agents: noAgents{}},
			[]string{"send_message"}, []string{"task"}},
		{"no agents", ToolsetOptions{Mode: auto}, []string{"read"}, []string{"task", "send_message"}},
		{"read-only sub-agent", ToolsetOptions{Mode: auto, Agents: noAgents{}, Spawn: true, Subagent: true},
			[]string{"read", "grep", "task"}, []string{"edit", "shell", "lint"}},
		{"writing sub-agent", ToolsetOptions{Mode: auto, Subagent: true, Write: true},
			[]string{"edit", "shell"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.opts.Shells, c.opts.Todos = tools.NewShells(), &tools.Todos{}
			ts := h.Toolset(c.opts)
			names := ts.Reg.Names()
			for _, n := range c.has {
				if !slices.Contains(names, n) {
					t.Errorf("missing %s in %v", n, names)
				}
			}
			for _, n := range c.lacks {
				if slices.Contains(names, n) {
					t.Errorf("has %s", n)
				}
			}
			if ts.Rules["lint"] != "ask" || ts.Rules["shell"] != "ask" {
				t.Errorf("rules %v", ts.Rules)
			}
		})
	}
	if len(*warns) == 0 || !strings.Contains((*warns)[0], "tools.commands.read") {
		t.Fatalf("warnings %q", *warns)
	}
}

func TestBuildErrors(t *testing.T) {
	cases := []struct {
		name, mode, prov, guard, want string
	}{
		{"unknown mode", "nope", "test", "", "Unknown mode nope"},
		{"unknown provider", "auto", "nope", "", `no provider named "nope"`},
		{"broken guard", "auto", "test", "paths: [unclosed\n", "guard.yaml"},
		{"fine", "auto", "test", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := testHost(t, `
agent:
  providers:
    test: { type: openai, base_url: "http://localhost:1/v1", model: m, context_window: 1234 }
`)
			if c.guard != "" {
				p := ProjectGuardPath(h.Root)
				_ = os.MkdirAll(filepath.Dir(p), 0o755)
				if err := os.WriteFile(p, []byte(c.guard), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s := agent.NewSession(c.mode, c.prov)
			ag, err := h.Build(Turn{Session: s, Mode: c.mode, Provider: c.prov,
				Tools: ToolsetOptions{Shells: tools.NewShells(), Todos: &tools.Todos{}}})
			if c.want != "" {
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("err %v, want %q", err, c.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ag.Model != "m" || ag.ContextWindow != 1234 || ag.Guard == nil || ag.Compaction == nil {
				t.Fatalf("agent %+v", ag)
			}
		})
	}
}

func TestSubagentPromptAndReport(t *testing.T) {
	if p := SubagentPrompt(false); !strings.Contains(p, "can read but not edit") {
		t.Fatal(p)
	}
	if p := SubagentPrompt(true); !strings.Contains(p, "may edit files") {
		t.Fatal(p)
	}
	parent := agent.NewSession("auto", "test")
	parent.Root, parent.ProjectID = "/w", "p1"
	s := NewSubagentSession(parent, "auto", "test", tools.Task{Description: "look"})
	if s.ParentID != parent.ID || s.Root != "/w" || s.ProjectID != "p1" || s.Title != "look" {
		t.Fatalf("child %+v", s)
	}
	if r := Report(t.Context(), s, nil); !r.IsError || !strings.Contains(r.Text, "without a report") {
		t.Fatalf("report %+v", r)
	}
}

func TestProviderTypesMatchPresets(t *testing.T) {
	want := append([]string{"anthropic", "openai", "google", "azure"}, provider.PresetNames()...)
	got := slices.Clone(config.ProviderTypes)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("config.ProviderTypes = %v, want %v", got, want)
	}
}

func TestContextWindowFromModels(t *testing.T) {
	h, warns := testHost(t, `
agent:
  providers:
    local: { type: ollama, model: "llama3.3:70b" }
    pinned: { type: ollama, model: "llama3.3:70b", context_window: 4096 }
    anthropic: { type: anthropic, model: claude-opus-5-5 }
`)
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config.UserDir(), "models.json"),
		`[{"id": "llama3.3:70b", "context_window": 131072}, {"id": "claude-opus-5-5", "provider": "anthropic", "context_window": 200000}]`)
	write(filepath.Join(h.Root, ".hm", "models.json"), `[{"id": "llama3.3:70b", "provider": "local", "context_window": 65536}]`)
	cases := map[string]int{"local": 65536, "pinned": 4096, "anthropic": 200_000}
	for name, want := range cases {
		if got := h.ContextWindow(name); got != want {
			t.Errorf("ContextWindow(%s) = %d, want %d", name, got, want)
		}
	}
	if len(*warns) > 0 {
		t.Errorf("warnings %v", *warns)
	}

	// A broken file is reported and the other still applies.
	write(filepath.Join(h.Root, ".hm", "models.json"), `[{`)
	h.Registry = nil
	if got := h.ContextWindow("local"); got != 131072 {
		t.Errorf("after a broken project file: %d", got)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0], "models.json") {
		t.Errorf("warnings %v", *warns)
	}
}
