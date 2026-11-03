package app

import (
	"path/filepath"
	"testing"

	"github.com/mirzakhany/yoga/input"
)

// TestEditorLineShortcuts drives the line commands through hm's own key
// layer, which runs before the editor sees a key.
func TestEditorLineShortcuts(t *testing.T) {
	root := project(t)
	writeFile(t, filepath.Join(root, "x.go"), "package x\n\nvar a = 1\nvar b = 2\n")
	h := newHarness(t, root, false)
	h.app.openFile(filepath.Join(root, "x.go"))
	h.frame()
	ed := h.app.curEditor()
	ed.Focus()
	ed.SetSelection(ed.Offset(2, 0), ed.Offset(2, 0))
	h.press(input.KeyDown, input.ModAlt)
	if got := string(ed.Bytes()); got != "package x\n\nvar b = 2\nvar a = 1\n" {
		t.Fatalf("⌥↓: %q", got)
	}
	h.press(input.KeySlash, input.ModSuper)
	if got := string(ed.Bytes()); got != "package x\n\nvar b = 2\n// var a = 1\n" {
		t.Fatalf("⌘/: %q", got)
	}
	h.press(input.KeyK, input.ModSuper|input.ModShift)
	if got := string(ed.Bytes()); got != "package x\n\nvar b = 2\n" {
		t.Fatalf("⌘⇧K: %q", got)
	}
}
