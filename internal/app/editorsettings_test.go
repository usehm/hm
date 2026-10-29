package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
)

func TestLanguageEditorSettings(t *testing.T) {
	root := project(t)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), `
languages:
  markdown:
    editor: { tab_width: 3, trim_trailing_whitespace: true, auto_indent: none }
`)
	y := filepath.Join(root, "a.yaml")
	md := filepath.Join(root, "notes.md")
	writeFile(t, y, "a:\n")
	writeFile(t, md, "x  \ny\t\n")
	h := newHarness(t, root, false)

	h.app.openFile(y)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	if ed.TabWidth() != 2 || !ed.IndentSpaces() {
		t.Fatalf("yaml: tab %d spaces %v, want 2 and spaces", ed.TabWidth(), ed.IndentSpaces())
	}
	// advanced auto indent from the defaults: a level after the colon.
	ed.Focus()
	ed.SetSelection(2, 2)
	h.press(input.KeyEnter, 0)
	if got := string(ed.Bytes()); got != "a:\n  \n" {
		t.Fatalf("yaml enter gave %q", got)
	}

	h.app.openFile(md)
	h.frame()
	ed = h.app.ws.ActiveEditor()
	if ed.TabWidth() != 3 {
		t.Fatalf("markdown tab %d, want the project's 3", ed.TabWidth())
	}
	ed.SetText("x  \ny\t\n")
	h.frame()
	h.press(input.KeyS, input.ModSuper)
	h.until("trimmed save", 5*time.Second, func() bool { return !ed.Modified() })
	if got, _ := os.ReadFile(md); string(got) != "x\ny\n" {
		t.Fatalf("saved %q, want trailing blanks trimmed", got)
	}

	// Editing the project config reapplies to open editors.
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), "languages:\n  markdown:\n    editor: { tab_width: 6 }\n")
	h.until("reload", 5*time.Second, func() bool { return ed.TabWidth() == 6 })
}
