package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/lsp"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeTool(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	p := filepath.Join(t.TempDir(), name)
	writeFile(t, p, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A new language comes from YAML alone: extensions, grammar and a
// formatter chain that runs on save in order.
func TestLanguageFromYAMLFormatsOnSave(t *testing.T) {
	root := project(t)
	upper := fakeTool(t, "upper", "tr a-z A-Z")
	stamp := fakeTool(t, "stamp", `printf '%s\n' "-- formatted" >> "$1"`)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), `
languages:
  shout:
    extensions: [.shout]
    highlight: sql
    formatters:
      - { command: `+upper+`, stdin: true, on: [save] }
      - { command: `+stamp+`, args: ["{file}"], on: [save, manual] }
`)
	src := filepath.Join(root, "a.shout")
	writeFile(t, src, "select 1\n")
	h := newHarness(t, root, false)
	h.app.openFile(src)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	ed.SetText("select 2\n")
	h.frame()
	h.press(input.KeyS, input.ModSuper)
	h.until("formatted save", 5*time.Second, func() bool { return !ed.Modified() })
	got, _ := os.ReadFile(src)
	if string(got) != "SELECT 2\n-- formatted\n" {
		t.Fatalf("saved %q", got)
	}
	if string(ed.Bytes()) != string(got) {
		t.Fatal("buffer should hold the formatted text")
	}
}

func TestFailingFormatterKeepsBuffer(t *testing.T) {
	root := project(t)
	bad := fakeTool(t, "bad", `echo "boom: line 1" >&2; exit 1`)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), `
languages:
  go:
    formatters: [{ command: `+bad+`, stdin: true, on: [save] }]
`)
	h := newHarness(t, root, false)
	main := filepath.Join(root, "main.go")
	h.app.openFile(main)
	ed := h.app.ws.ActiveEditor()
	ed.SetText("package main\n\nfunc   main() {}\n")
	h.frame()
	h.press(input.KeyS, input.ModSuper)
	h.until("save after failed format", 5*time.Second, func() bool { return !ed.Modified() })
	if got, _ := os.ReadFile(main); string(got) != "package main\n\nfunc   main() {}\n" {
		t.Fatalf("saved %q", got)
	}
	if !hasNotification(h, "Format failed") || !strings.Contains(h.app.output.String(), "boom: line 1") {
		t.Fatal("expected the formatter's stderr in a notification and the Output panel")
	}
}

func goProject(t *testing.T) string {
	t.Helper()
	root := project(t)
	writeFile(t, filepath.Join(root, "util", "strings.go"), "package util\n\n// Shout returns s louder.\nfunc Shout(s string) string { return s + \"!\" }\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nimport \"example.com/demo/util\"\n\nfunc main() { println(util.Shout(\"hi\")) }\n")
	return root
}

func TestGoToDefinitionGo(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	root := goProject(t)
	h := newHarness(t, root, false)
	main := filepath.Join(root, "main.go")
	h.app.openFile(main)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	off := strings.Index(string(ed.Bytes()), "Shout")
	waitDefinition(t, h, ed.Path, off, filepath.Join(root, "util", "strings.go"), "Shout")
}

func TestGoToDefinitionTypeScript(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}
	root := project(t)
	writeFile(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions": {"strict": true}}`)
	writeFile(t, filepath.Join(root, "lib.ts"), "export function greet(name: string): string {\n  return `hi ${name}`\n}\n")
	writeFile(t, filepath.Join(root, "app.tsx"), "import { greet } from './lib'\n\nexport const el = greet('x')\n")
	h := newHarness(t, root, false)
	app := filepath.Join(root, "app.tsx")
	h.app.openFile(app)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	off := strings.LastIndex(string(ed.Bytes()), "greet")
	waitDefinition(t, h, ed.Path, off, filepath.Join(root, "lib.ts"), "greet")
}

// waitDefinition retries go-to-definition until the server has loaded the
// workspace, then checks that it landed on want in file.
func waitDefinition(t *testing.T, h *harness, from string, off int, file, want string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		ed := h.app.bufs.Get(from)
		h.app.ws.Open(from)
		h.app.definition(ed, off)
		end := time.Now().Add(3 * time.Second)
		for time.Now().Before(end) {
			time.Sleep(50 * time.Millisecond)
			h.frame()
			if cur := h.app.ws.ActiveEditor(); cur != nil && cur.Path == file {
				lo, hi := cur.Selection()
				if got := string(cur.Bytes()[lo:hi]); got != want {
					t.Fatalf("selected %q, want %q", got, want)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("definition never opened %s; notifications: %v", file, h.app.notifs.items)
		}
	}
}

func TestRenameAcrossFiles(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	root := goProject(t)
	h := newHarness(t, root, false)
	util := filepath.Join(root, "util", "strings.go")
	h.app.openFile(filepath.Join(root, "main.go"))
	h.app.openFile(util)
	h.frame()
	ed := h.app.bufs.Get(util)
	off := strings.Index(string(ed.Bytes()), "Shout(s")
	pos := h.app.position(ed, off)
	m := h.app.lang.Manager()
	var we lsp.WorkspaceEdit
	h.until("gopls rename", 60*time.Second, func() bool {
		ctx, cancel := ctxTimeout(2 * time.Second)
		defer cancel()
		var err error
		we, err = m.Rename(ctx, util, pos, "Yell")
		return err == nil && len(we.Edits()) == 2
	})
	n, err := h.app.applyWorkspaceEdit(we, m.Encoding(util))
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if !strings.Contains(string(h.app.bufs.Get(filepath.Join(root, "main.go")).Bytes()), "util.Yell(") {
		t.Fatal("main.go not renamed")
	}
}

// Formatting a new Go file only appends gofmt's final newline; applying that
// edit used to panic in the editor and take the app down.
func TestFormatOnSaveNewGoFile(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	root := project(t)
	src := filepath.Join(root, "new.go")
	writeFile(t, src, "")
	h := newHarness(t, root, false)
	h.app.openFile(src)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	h.until("gopls formatting", 60*time.Second, func() bool {
		return h.app.lang.Manager().Supports(src, "documentFormattingProvider")
	})
	ed.SetText("package main")
	h.frame()
	h.press(input.KeyS, input.ModSuper)
	h.until("formatted save", 30*time.Second, func() bool {
		got, _ := os.ReadFile(src)
		return !ed.Modified() && string(got) == "package main\n"
	})
	if string(ed.Bytes()) != "package main\n" {
		t.Fatalf("buffer %q", ed.Bytes())
	}
}

func TestProblemsPanelListsDiagnostics(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	root := goProject(t)
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() { undefinedThing() }\n")
	h := newHarness(t, root, false)
	h.app.openFile(filepath.Join(root, "main.go"))
	h.app.showPanel(panelProblems)
	h.until("diagnostics", 60*time.Second, func() bool {
		errs, _ := countProblems(h.app.problems())
		return errs > 0
	})
	ps := h.app.problems()
	if !strings.Contains(ps[0].diag.Message, "undefinedThing") {
		t.Fatalf("problem %+v", ps[0].diag)
	}
}

func TestMissingServerOffersInstall(t *testing.T) {
	root := project(t)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), `
languages:
  zig:
    extensions: [.zig]
    lsp:
      command: hm-no-such-zls
      install: [{ tool: sh, args: ["-c", "true"] }]
`)
	src := filepath.Join(root, "a.zig")
	writeFile(t, src, "const x = 1;\n")
	h := newHarness(t, root, false)
	h.app.openFile(src)
	h.until("missing-server notice", 5*time.Second, func() bool {
		return hasNotification(h, "Zig language server not installed")
	})
}
