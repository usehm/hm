// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"path/filepath"
	"testing"

	codeeditor "github.com/yogaui/editor"

	"github.com/yogaui/yoga/input"
)

func TestVimKeymapInTheEditor(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	main := filepath.Join(root, "main.go")
	h.app.openFile(main)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	ed.SetSelection(0, 0)

	h.kb.TypeRune('x') // default keymap: typing types
	h.frame()
	if string(ed.Bytes()[:2]) != "xp" {
		t.Fatalf("default keymap: %q", ed.Bytes()[:10])
	}
	ed.Undo()

	h.app.setSetting([]string{"editor", "keymap"}, "vim")
	h.frame()
	if ed.CaretStyle != codeeditor.CaretBlock {
		t.Fatal("normal mode should show a block caret")
	}
	ed.SetSelection(0, 0)
	for _, r := range "dw" {
		h.kb.TypeRune(r)
	}
	h.frame()
	if got := string(ed.Bytes()); got[:5] != "main\n" {
		t.Fatalf("dw: %q", got)
	}
	h.kb.TypeRune('i')
	h.frame()
	if ed.CaretStyle != codeeditor.CaretBar {
		t.Fatal("insert mode should show a bar caret")
	}
	h.kb.TypeRune('Z')
	h.frame()
	h.press(input.KeyEscape, 0)
	if got := string(ed.Bytes()); got[:6] != "Zmain\n" {
		t.Fatalf("insert: %q", got)
	}
	for _, r := range ":w" {
		h.kb.TypeRune(r)
	}
	h.press(input.KeyEnter, 0)
	if ed.Modified() {
		t.Fatal(":w should save")
	}
}
