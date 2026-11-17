// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"path/filepath"
	"testing"

	"github.com/usehm/hm/internal/diff"
)

func TestHunkAt(t *testing.T) {
	hunks := []diff.Hunk{
		{A0: 1, A1: 2, B0: 1, B1: 2}, // line 1 modified
		{A0: 4, A1: 6, B0: 4, B1: 4}, // two lines deleted before line 4
		{A0: 8, A1: 8, B0: 6, B1: 8}, // lines 6 and 7 added
	}
	for line, want := range map[int]int{0: -1, 1: 0, 4: 1, 5: -1, 6: 2, 7: 2} {
		if got := hunkAt(hunks, line); got != want {
			t.Errorf("line %d: hunk %d, want %d", line, got, want)
		}
	}
}

func TestChangePeekReverts(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	path := filepath.Join(root, "main.go")
	h.app.openFile(path)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	base := "package main\n\nfunc main() {}\n\nfunc a() {}\n"
	h.app.gitst.bases[path] = &gitBase{data: []byte(base), loaded: true, tracked: true}
	ed.ApplyText("package main\n\nfunc main() { println() }\n\nfunc a() {}\nfunc b() {}\n")

	h.app.showChange(ed, 2)
	p := h.app.peek
	if p == nil || len(p.hunks) != 2 || p.cur != 0 {
		t.Fatalf("peek %+v", p)
	}
	h.app.stepChange(ed, 1)
	if h.app.peek == nil || h.app.peek.cur != 1 {
		t.Fatalf("next change: %+v", h.app.peek)
	}
	if line, _ := ed.LineCol(ed.Caret()); line != 5 {
		t.Fatalf("caret on line %d, want 5", line)
	}
	h.app.revertChange(ed)
	if got := string(ed.Bytes()); got != "package main\n\nfunc main() { println() }\n\nfunc a() {}\n" {
		t.Fatalf("after reverting the addition: %q", got)
	}
	if h.app.peek != nil {
		t.Fatal("revert closes the peek")
	}
	h.app.showChange(ed, 2)
	h.app.revertChange(ed)
	if got := string(ed.Bytes()); got != base {
		t.Fatalf("after reverting the edit: %q", got)
	}
	ed.Undo()
	if got := string(ed.Bytes()); got == base {
		t.Fatal("a revert undoes in one step")
	}
}

func TestChangePeekRevertsADeletion(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	path := filepath.Join(root, "main.go")
	h.app.openFile(path)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	for _, tc := range []struct{ base, cur string }{
		{"x\ny\nz\n", "x\nz\n"},
		{"x\ny\nz\n", "x\n"},
		{"x\ny\n", "y\n"},
	} {
		h.app.gitst.bases[path] = &gitBase{data: []byte(tc.base), loaded: true, tracked: true}
		ed.ApplyText(tc.cur)
		_, _, hunks, _ := h.app.changeHunks(ed)
		if len(hunks) != 1 {
			t.Fatalf("%q: hunks %+v", tc.cur, hunks)
		}
		h.app.showChange(ed, hunks[0].B0)
		h.app.revertChange(ed)
		if got := string(ed.Bytes()); got != tc.base {
			t.Errorf("revert %q: got %q, want %q", tc.cur, got, tc.base)
		}
	}
}
