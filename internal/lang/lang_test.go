package lang

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/highlight"
	"github.com/mirzakhany/yoga/lsp"

	"github.com/mirzakhany/hm/internal/config"
)

func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	p := filepath.Join(t.TempDir(), "fmt.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFormatChainRunsInOrder(t *testing.T) {
	upper := script(t, `tr a-z A-Z`)
	// A file formatter: appends a line to {file}.
	appendLine := script(t, `echo "// done" >> "$1"`)
	path := filepath.Join(t.TempDir(), "x.go")
	out, err := Format(context.Background(), FormatRequest{
		Path: path, Src: []byte("abc\n"), Trigger: "save",
		Formatters: []config.Formatter{
			{Command: upper, Stdin: true, On: []string{"save"}},
			{Command: "never-runs", On: []string{"manual"}},
			{Command: appendLine, Args: []string{"{file}"}, On: []string{"save", "manual"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "ABC\n// done\n" {
		t.Fatalf("out %q", out)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 0 {
		t.Fatalf("file formatter left %v behind", entries)
	}
}

func TestFormatFailureAndMissing(t *testing.T) {
	bad := script(t, `echo "syntax error at 3:1" >&2; exit 2`)
	_, err := Format(context.Background(), FormatRequest{
		Path: "/tmp/x.go", Src: []byte("x"), Trigger: "save",
		Formatters: []config.Formatter{{Command: bad, Stdin: true, On: []string{"save"}}},
	})
	var fe *FormatError
	if !errors.As(err, &fe) || fe.Stderr != "syntax error at 3:1" {
		t.Fatalf("err %v", err)
	}
	_, err = Format(context.Background(), FormatRequest{
		Path: "/tmp/x.go", Src: []byte("x"), Trigger: "save",
		Formatters: []config.Formatter{{Command: "hm-no-such-formatter", On: []string{"save"},
			Install: []config.Installer{{Tool: "npm", Args: []string{"i", "-g", "x"}}}}},
	})
	var me *MissingError
	if !errors.As(err, &me) || len(me.Install) != 1 {
		t.Fatalf("err %v", err)
	}
	_, err = Format(context.Background(), FormatRequest{Path: "/tmp/x.go", Trigger: "paste",
		Formatters: []config.Formatter{{Command: bad, On: []string{"save"}}}})
	if !errors.Is(err, ErrNothingToRun) {
		t.Fatalf("err %v", err)
	}
}

func TestFormatFallsBackToLSP(t *testing.T) {
	called := false
	out, err := Format(context.Background(), FormatRequest{Path: "/tmp/x.go", Src: []byte("a"), Trigger: "save",
		LSP: func(context.Context, []byte) ([]byte, error) { called = true; return []byte("b"), nil }})
	if err != nil || !called || string(out) != "b" {
		t.Fatal(out, err)
	}
}

func TestApplyRegistersFromConfig(t *testing.T) {
	cfg, ps := config.Load(config.Layer{Name: "user", Data: []byte(`
languages:
  zig:
    extensions: [.zig]
    highlight: rust
    lsp: { command: zls, settings: { zls: { enable_snippets: true } } }
  typescript:
    lsp: { disabled: true }
`)})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	s := New(nil)
	s.Apply(cfg.Languages)
	defer s.Apply(nil)

	sc, ok := lsp.ForPath("a.zig")
	if !ok || sc.Command != "zls" || sc.LanguageID != "zig" || sc.Settings["zls"] == nil {
		t.Fatalf("zig server %+v %v", sc, ok)
	}
	if _, ok := lsp.ForPath("a.ts"); ok {
		t.Fatal("disabled server still registered")
	}
	if sc, _ := lsp.ForPath("a.sh"); sc.LanguageID != "shellscript" {
		t.Fatalf("language_id: %+v", sc)
	}
	h := highlight.ForPath("a.zig")
	defer h.Close()
	if _, noop := h.(highlight.Noop); noop {
		t.Fatal("zig should highlight with the rust grammar")
	}
	if l, ok := s.For("/x/y.ZIG"); !ok || l.Name != "zig" {
		t.Fatalf("For: %+v", l)
	}
	if sc, ok := lsp.ForPath("/x/Makefile"); !ok || sc.Command != "make-ls" || sc.LanguageID != "makefile" {
		t.Fatalf("Makefile server %+v %v", sc, ok)
	}
	if l, ok := s.For("/x/GNUmakefile"); !ok || l.Name != "make" || len(l.Formatters) == 0 {
		t.Fatalf("For(GNUmakefile): %+v", l)
	}
	mk := highlight.ForPath("/x/Makefile")
	if _, noop := mk.(highlight.Noop); noop {
		t.Fatal("Makefile should be highlighted")
	}
	mk.Close()
	for _, p := range []string{"/x/Cargo.toml", "/x/.gitignore", "/x/.env.local", "/x/.editorconfig",
		"/x/Dockerfile", "/x/go.mod", "/x/fix.patch", "/x/main.c", "/x/a.hpp", "/x/A.cs", "/x/init.lua",
		"/x/Gemfile", "/x/index.php"} {
		h := highlight.ForPath(p)
		if _, noop := h.(highlight.Noop); noop {
			t.Errorf("%s should be highlighted", p)
		}
		h.Close()
	}
	if sc, ok := lsp.ForPath("/x/Cargo.toml"); !ok || sc.Command != "taplo" {
		t.Fatalf("TOML server %+v %v", sc, ok)
	}
	if l, ok := s.For("/x/.dockerignore"); !ok || l.Name != "gitignore" {
		t.Fatalf("For(.dockerignore): %+v", l)
	}
	if _, ok := s.For("/x/LICENSE"); ok {
		t.Fatal("a file without an extension matched a language")
	}
	s.Apply(nil)
	if _, ok := lsp.ForPath("a.zig"); ok {
		t.Fatal("Apply(nil) should unregister")
	}
}

func TestHighlightQueriesExtendTheGrammar(t *testing.T) {
	q := filepath.Join(t.TempDir(), "extra.scm")
	if err := os.WriteFile(q, []byte(`((identifier) @constant (#match? @constant "^[A-Z][A-Z_]+$"))`), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.scm")
	if err := os.WriteFile(bad, []byte(`(no_such_node) @x`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(nil)
	defer s.Apply(nil)
	errs := s.Apply(map[string]config.Language{
		"go":   {Extensions: []string{".go"}, HighlightQueries: []string{q}},
		"rust": {Extensions: []string{".rs"}, HighlightQueries: []string{bad}},
	})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "languages.rust.highlight_queries") {
		t.Fatalf("errs %v, want one for rust", errs)
	}
	h := highlight.ForPath("a.go")
	defer h.Close()
	src := "package p\n\nvar x = MAX_N\n"
	h.Update([]byte(src))
	for range 300 {
		if toks, ok := h.Poll(); ok {
			for _, tk := range toks {
				if src[tk.Start:tk.End] == "MAX_N" && tk.Class == highlight.ClassConstant {
					return
				}
			}
			t.Fatalf("MAX_N not a constant: %v", toks)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no tokens")
}
