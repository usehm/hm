package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirzakhany/yoga/input"
)

func TestNavigationBackAndForward(t *testing.T) {
	root := project(t)
	writeFile(t, filepath.Join(root, "long.go"), "package main\n"+strings.Repeat("// line\n", 50))
	h := newHarness(t, root, false)
	main, long := filepath.Join(root, "main.go"), filepath.Join(root, "long.go")
	h.app.openFile(main)
	h.frame()
	h.app.openFile(long)
	h.frame()
	ed := h.app.bufs.Get(long)
	ed.SetSelection(ed.Offset(3, 0), ed.Offset(3, 0))
	h.frame()
	ed.SetSelection(ed.Offset(40, 0), ed.Offset(40, 0)) // a jump within the file
	h.frame()

	h.press(input.KeyMinus, input.ModCtrl)
	if line, _ := ed.LineCol(ed.Caret()); line != 3 {
		t.Fatalf("back: line %d, want 3", line)
	}
	h.press(input.KeyMouseBack, 0)
	if p := h.app.ws.ActiveEditor().Path; p != main {
		t.Fatalf("back again: %s, want main.go", p)
	}
	h.press(input.KeyMouseForward, 0)
	h.press(input.KeyMinus, input.ModCtrl|input.ModShift)
	if p, line := h.app.ws.ActiveEditor().Path, 0; p != long {
		t.Fatalf("forward: %s", p)
	} else if line, _ = ed.LineCol(ed.Caret()); line != 40 {
		t.Fatalf("forward twice: line %d, want 40", line)
	}
}
