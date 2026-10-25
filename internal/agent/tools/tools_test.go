package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirzakhany/hm/internal/search"
)

type env struct{ root string }

func (e env) Root() string                      { return e.root }
func (e env) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (e env) Diagnostics(string) []string       { return []string{"a.go:1:1: boom"} }
func (e env) Search(ctx context.Context, q search.Query, max int) ([]search.FileResult, error) {
	var out []search.FileResult
	err := search.Run(ctx, e.root, q, search.Options{Engine: "builtin", MaxResults: max}, func(fr search.FileResult) bool {
		out = append(out, fr)
		return true
	})
	return out, err
}

func setup(t *testing.T) env {
	t.Helper()
	root := t.TempDir()
	for p, s := range map[string]string{
		"a.go":            "package a\n\nfunc A() int { return 1 }\nfunc B() int { return 1 }\n",
		"cmd/x/main.go":   "package main\n",
		"cmd/x/main_test": "x\n",
	} {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(s), 0o644)
	}
	return env{root}
}

func run(t *testing.T, e env, tool Tool, input string) (Plan, Result) {
	t.Helper()
	p, err := tool.Prepare(e, json.RawMessage(input))
	if err != nil {
		return p, Result{Text: err.Error(), IsError: true}
	}
	if p.Run != nil {
		return p, p.Run(context.Background())
	}
	return p, Result{}
}

func TestEditRules(t *testing.T) {
	e := setup(t)
	_, r := run(t, e, editTool{}, `{"path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	if !r.IsError || !strings.Contains(r.Text, "appears 2 times") {
		t.Fatalf("ambiguous edit: %q", r.Text)
	}
	p, r := run(t, e, editTool{}, `{"path":"a.go","old_string":"return 1","new_string":"return 2","replace_all":true}`)
	if r.IsError || strings.Count(string(p.Edits[0].New), "return 2") != 2 || p.Category != CatEdit {
		t.Fatalf("replace_all: %q %+v", r.Text, p)
	}
	if b, _ := os.ReadFile(filepath.Join(e.root, "a.go")); strings.Contains(string(b), "return 2") {
		t.Fatal("prepare must not write")
	}
	_, r = run(t, e, editTool{}, `{"path":"a.go","old_string":"nope","new_string":"x"}`)
	if !r.IsError || !strings.Contains(r.Text, "not found") {
		t.Fatalf("missing: %q", r.Text)
	}
	p, r = run(t, e, multiEditTool{}, `{"path":"a.go","edits":[{"old_string":"func A","new_string":"func AA"},{"old_string":"func AA() int { return 1 }","new_string":"func AA() int { return 3 }"}]}`)
	if r.IsError || !strings.Contains(string(p.Edits[0].New), "func AA() int { return 3 }") {
		t.Fatalf("multi edit: %q %s", r.Text, p.Edits[0].New)
	}
	_, r = run(t, e, editTool{}, `{"path":"a.go","old_string":"x","new_string":"y","bogus":1}`)
	if !r.IsError || !strings.Contains(r.Text, "unknown field") {
		t.Fatalf("unknown field: %q", r.Text)
	}
}

func TestReadGlobGrep(t *testing.T) {
	e := setup(t)
	_, r := run(t, e, readTool{}, `{"path":"a.go","offset":3,"limit":1}`)
	if r.Text != "     3\tfunc A() int { return 1 }\n… 1 more lines; read again with offset 4\n" {
		t.Fatalf("read %q", r.Text)
	}
	_, r = run(t, e, globTool{}, `{"pattern":"cmd/**"}`)
	if r.Text != "cmd/x/main.go\ncmd/x/main_test" {
		t.Fatalf("glob %q", r.Text)
	}
	_, r = run(t, e, grepTool{}, `{"pattern":"func [AB]\\(","include":"*.go"}`)
	if r.Text != "a.go:3: func A() int { return 1 }\na.go:4: func B() int { return 1 }\n" {
		t.Fatalf("grep %q", r.Text)
	}
	_, r = run(t, e, diagnosticsTool{}, `{}`)
	if r.Text != "a.go:1:1: boom" {
		t.Fatalf("diagnostics %q", r.Text)
	}
}

func TestShellAndGitCategories(t *testing.T) {
	e := setup(t)
	sh := &shellTool{shells: NewShells()}
	p, r := run(t, e, sh, `{"command":"echo hi && exit 3"}`)
	if p.Category != CatShell || !r.IsError || !strings.Contains(r.Text, "hi") || !strings.Contains(r.Text, "exit status 3") {
		t.Fatalf("shell %+v %q", p, r.Text)
	}
	for cmd, want := range map[string]bool{
		"git push":                  true,
		"git -C x push origin main": true,
		"make && git reset --hard":  true,
		"git branch -D old":         true,
		"git status":                false,
		"git log --oneline | head":  false,
		"echo pushing":              false,
	} {
		if IsDestructiveGit(cmd) != want {
			t.Errorf("%q destructive=%v", cmd, !want)
		}
	}
	p, r = run(t, e, sh, `{"command":"echo bg; sleep 0.1","background":true}`)
	if r.IsError || !strings.Contains(r.Text, "bg1") {
		t.Fatalf("background %q", r.Text)
	}
}

func TestFilter(t *testing.T) {
	r := NewRegistry(Builtin(NewShells(), &Todos{})...)
	f := r.Filter([]string{"read", "git_*"})
	names := strings.Join(f.Names(), ",")
	if names != "read,git_status,git_diff,git_log" {
		t.Fatalf("filtered %s", names)
	}
	if len(r.Filter([]string{"all"}).Names()) != len(r.Names()) {
		t.Fatal("all")
	}
}
