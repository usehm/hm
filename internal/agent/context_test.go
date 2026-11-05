package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadContext(t *testing.T) {
	names := []string{"AGENTS.md", "CLAUDE.md"}
	cases := []struct {
		name     string
		files    map[string]string // under the temp dir; home is home/, the workspace home/src/proj
		opts     ContextOptions
		want     []string // Rel of each file, in order
		system   string   // Rel of SYSTEM.md in use
		appended bool
		warn     int
	}{
		{name: "none", opts: ContextOptions{Names: names}},
		{
			name: "outer folders first",
			files: map[string]string{
				"home/src/proj/AGENTS.md": "root", "home/src/AGENTS.md": "parent",
				"home/AGENTS.md": "home", "home/cfg/AGENTS.md": "user",
			},
			opts: ContextOptions{Names: names},
			want: []string{"~/cfg/AGENTS.md", "~/AGENTS.md", "~/src/AGENTS.md", "AGENTS.md"},
		},
		{
			name:  "stops at home",
			files: map[string]string{"AGENTS.md": "above home", "home/src/proj/AGENTS.md": "root"},
			opts:  ContextOptions{Names: names},
			want:  []string{"AGENTS.md"},
		},
		{
			name:  "CLAUDE.md is the fallback",
			files: map[string]string{"home/src/CLAUDE.md": "parent", "home/src/proj/AGENTS.md": "a", "home/src/proj/CLAUDE.md": "c"},
			opts:  ContextOptions{Names: names},
			want:  []string{"~/src/CLAUDE.md", "AGENTS.md"},
		},
		{
			name:  "names from config",
			files: map[string]string{"home/src/proj/AGENTS.md": "a", "home/src/proj/RULES.md": "r"},
			opts:  ContextOptions{Names: []string{"RULES.md"}},
			want:  []string{"RULES.md"},
		},
		{
			name:  "too big is skipped for the next name",
			files: map[string]string{"home/src/proj/AGENTS.md": strings.Repeat("x", 100), "home/src/proj/CLAUDE.md": "small"},
			opts:  ContextOptions{Names: names, MaxBytes: 50},
			want:  []string{"CLAUDE.md"},
			warn:  1,
		},
		{
			name:  "empty file is left out",
			files: map[string]string{"home/src/proj/AGENTS.md": "  \n"},
			opts:  ContextOptions{Names: names},
		},
		{
			name:   "SYSTEM.md replaces",
			files:  map[string]string{"home/cfg/SYSTEM.md": "You are terse."},
			opts:   ContextOptions{Names: names},
			system: "~/cfg/SYSTEM.md",
		},
		{
			name:     "project SYSTEM.md wins and appends",
			files:    map[string]string{"home/cfg/SYSTEM.md": "global", "home/src/proj/.hm/SYSTEM.md": "<!-- append -->\nAlso be terse."},
			opts:     ContextOptions{Names: names},
			system:   ".hm/SYSTEM.md",
			appended: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			root := filepath.Join(home, "src", "proj")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			for p, text := range tc.files {
				p = filepath.Join(dir, p)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			c := LoadContext(root, home, filepath.Join(home, "cfg"), tc.opts)
			var got []string
			for _, f := range c.Files {
				got = append(got, f.Rel)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("files %v, want %v", got, tc.want)
			}
			if len(c.Warnings) != tc.warn {
				t.Errorf("warnings %v, want %d", c.Warnings, tc.warn)
			}
			sys := ""
			if c.System != nil {
				sys = c.System.Rel
			}
			if sys != tc.system || c.Append != tc.appended {
				t.Errorf("system %q append %v, want %q %v", sys, c.Append, tc.system, tc.appended)
			}
		})
	}
}

func TestLoadContextOutsideHome(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "work", "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work", "AGENTS.md"), []byte("parent"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := LoadContext(root, filepath.Join(dir, "home"), filepath.Join(dir, "cfg"), ContextOptions{Names: []string{"AGENTS.md"}})
	if len(c.Files) != 1 || c.Files[0].Path != filepath.Join(dir, "work", "AGENTS.md") || c.Files[0].Rel != c.Files[0].Path {
		t.Fatalf("files %+v", c.Files)
	}
}

func TestSystemWithContext(t *testing.T) {
	ctx := &Context{Files: []ContextFile{{Rel: "~/src/AGENTS.md", Content: "outer"}, {Rel: "AGENTS.md", Content: "inner\n"}}}
	sys := System(Mode{Name: "auto", Prompt: "mode text"}, "/r", time.Now(), &Project{Title: "P"}, ctx, "extra")
	order := []string{basePrompt, "# Environment", "# Project instructions", "## ~/src/AGENTS.md\nouter", "## AGENTS.md\ninner", "# Project context", "# Mode: auto", "extra"}
	at := 0
	for _, part := range order {
		i := strings.Index(sys[at:], part)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", part, sys)
		}
		at += i + len(part)
	}

	cases := []struct {
		name       string
		ctx        *Context
		base, lack string
	}{
		{"no context", nil, basePrompt, "# Project instructions"},
		{"replace", &Context{System: &ContextFile{Content: "Be terse.\n"}}, "Be terse.\n\n# Environment", basePrompt},
		{"append", &Context{System: &ContextFile{Content: "Be terse."}, Append: true}, basePrompt + "\n\nBe terse.\n\n# Environment", "<!-- append -->"},
	}
	for _, tc := range cases {
		sys := System(Mode{Name: "auto"}, "/r", time.Now(), nil, tc.ctx, "")
		if !strings.HasPrefix(sys, tc.base) || strings.Contains(sys, tc.lack) {
			t.Errorf("%s:\n%s", tc.name, sys)
		}
	}
}
