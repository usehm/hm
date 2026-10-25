package lang

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
	s.Apply(nil)
	if _, ok := lsp.ForPath("a.zig"); ok {
		t.Fatal("Apply(nil) should unregister")
	}
}
