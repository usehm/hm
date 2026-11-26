// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yogaui/yoga/input"
)

func searchProject(t *testing.T) (*harness, string) {
	t.Helper()
	root := project(t)
	writeFile(t, filepath.Join(root, "a.go"), "package main\n\nvar x = call(1, 2)\nvar y = call(3, 4)\n")
	writeFile(t, filepath.Join(root, "b.go"), "package main\n\nvar z = call(5, 6)\n")
	h := newHarness(t, root, false)
	return h, root
}

func waitSearch(t *testing.T, h *harness, total int) {
	t.Helper()
	h.until("search results", 5*time.Second, func() bool {
		return !h.app.search.running && h.app.search.pending.IsZero() && h.app.search.total == total
	})
}

func TestCmdShiftFOpensSearchAndRegexGroupsReplace(t *testing.T) {
	h, root := searchProject(t)
	h.app.openFile(filepath.Join(root, "main.go"))
	h.frame()
	h.press(input.KeyF, input.ModSuper|input.ModShift)
	if !h.app.search.open || !h.c.Dialogs().Open {
		t.Fatal("⌘⇧F should open the search dialog")
	}
	s := &h.app.search
	s.q.Pattern, s.q.Regex, s.replace = `call\((\d+), (\d+)\)`, true, "call($2, $1)"
	h.app.runSearch()
	waitSearch(t, h, 3)
	if len(s.results) != 2 || filepath.Base(s.results[0].Path) != "a.go" {
		t.Fatalf("results %+v", s.results)
	}
	// The first result is selected and shown in the preview.
	if s.preview != filepath.Join(root, "a.go") {
		t.Fatalf("preview %q", s.preview)
	}
	ed := h.app.bufs.Get(s.preview)
	lo, hi := ed.Selection()
	if string(ed.Bytes()[lo:hi]) != "call(1, 2)" {
		t.Fatalf("selected %q", ed.Bytes()[lo:hi])
	}
	h.app.replaceOne(0, 1)
	waitSearch(t, h, 3)
	if !strings.Contains(string(ed.Bytes()), "var y = call(4, 3)") || !strings.Contains(string(ed.Bytes()), "call(1, 2)") {
		t.Fatalf("replace one:\n%s", ed.Bytes())
	}
}

func TestEditInPreviewMarksFileDirty(t *testing.T) {
	h, root := searchProject(t)
	h.app.openSearch()
	s := &h.app.search
	s.q.Pattern = "call(5"
	h.app.runSearch()
	waitSearch(t, h, 1)
	b := filepath.Join(root, "b.go")
	ed := h.app.bufs.Get(b)
	if ed == nil || s.preview != b {
		t.Fatal("b.go should be in the preview")
	}
	ed.SetSelection(0, 0)
	h.kb.TypeRune('/')
	h.kb.TypeRune('/')
	ed.Focus()
	h.frame()
	ed.ApplyText("// edited\n" + string(ed.Bytes()))
	h.frame()
	h.c.Dialogs().Close()
	h.app.closeSearch()
	if !h.app.hasTab(b) {
		t.Fatal("an edited preview file should get a tab")
	}
	i := -1
	for j, tab := range h.app.ws.Tabs {
		if tab.Path == b {
			i = j
		}
	}
	if !h.app.ws.Modified(i) {
		t.Fatal("the tab should be dirty")
	}
}

// TestEditInPreviewKeepsFile edits away the previewed file's only match: the
// file must stay in the results and in the preview rather than the search
// moving on to another file mid-edit.
func TestEditInPreviewKeepsFile(t *testing.T) {
	h, root := searchProject(t)
	h.app.openSearch()
	s := &h.app.search
	s.q.Pattern = "call"
	h.app.runSearch()
	waitSearch(t, h, 3)
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	h.app.selectMatch(1, 0)
	ed := h.app.bufs.Get(b)
	if s.preview != b || ed == nil {
		t.Fatalf("preview %q", s.preview)
	}
	ed.ApplyText("package main\n\nvar z = invoke(5, 6)\n")
	lo, hi := ed.Selection()
	waitSearch(t, h, 2)
	if s.preview != b || len(s.results) != 2 || s.results[1].Path != b || len(s.results[1].Matches) != 0 {
		t.Fatalf("preview %q, results %+v", s.preview, s.results)
	}
	if s.selFile != 1 || s.selMatch != 0 {
		t.Fatalf("selection %d/%d", s.selFile, s.selMatch)
	}
	if l, r := ed.Selection(); l != lo || r != hi {
		t.Fatal("the rescan should leave the caret alone")
	}
	// Typing matches back in counts them again.
	ed.ApplyText("package main\n\nvar z = call(5, 6) + call(7, 8)\n")
	waitSearch(t, h, 4)
	if s.preview != b || len(s.results[1].Matches) != 2 || s.selFile != 1 {
		t.Fatalf("preview %q, results %+v, selFile %d", s.preview, s.results, s.selFile)
	}

	// Stepping past an empty file skips it.
	ed.ApplyText("package main\n")
	waitSearch(t, h, 2)
	h.app.step(1)
	if s.preview != a || s.selFile != 0 || s.selMatch != 0 {
		t.Fatalf("step went to %q %d/%d", s.preview, s.selFile, s.selMatch)
	}
}

// TestStepWithNoMatchesLeft must return rather than spin when every file in
// the results has had its matches edited away.
func TestStepWithNoMatchesLeft(t *testing.T) {
	h, root := searchProject(t)
	h.app.openSearch()
	s := &h.app.search
	s.q.Pattern = "call(5"
	h.app.runSearch()
	waitSearch(t, h, 1)
	ed := h.app.bufs.Get(filepath.Join(root, "b.go"))
	ed.ApplyText("package main\n")
	waitSearch(t, h, 0)
	h.app.step(1)
	h.app.step(-1)
	if len(s.results) != 1 {
		t.Fatalf("results %+v", s.results)
	}
}

// TestSearchFieldDeletesByWord types in the search box and deletes a word
// with the platform's chord, ⌥⌫ on macOS and Ctrl+Backspace elsewhere (#1).
func TestSearchFieldDeletesByWord(t *testing.T) {
	h, _ := searchProject(t)
	h.app.openSearch()
	h.frame()
	h.typeText("call foo")
	mods := input.ModCtrl
	if runtime.GOOS == "darwin" {
		mods = input.ModAlt
	}
	h.press(input.KeyBackspace, mods)
	if p := h.app.search.q.Pattern; p != "call " {
		t.Fatalf("pattern %q", p)
	}
}

func TestReplaceAllIsOneUndoPerFile(t *testing.T) {
	h, root := searchProject(t)
	h.app.openSearch()
	s := &h.app.search
	s.q.Pattern, s.replace = "call", "invoke"
	h.app.runSearch()
	waitSearch(t, h, 3)
	s.showReplace, s.confirm = true, true
	h.frame()
	h.app.replaceAll()
	waitSearch(t, h, 0)
	for _, name := range []string{"a.go", "b.go"} {
		p := filepath.Join(root, name)
		ed := h.app.bufs.Get(p)
		if ed == nil || strings.Contains(string(ed.Bytes()), "call") || !h.app.hasTab(p) {
			t.Fatalf("%s not replaced or has no tab", name)
		}
		ed.Undo()
		if strings.Contains(string(ed.Bytes()), "invoke") {
			t.Fatalf("%s: one undo should restore the file:\n%s", name, ed.Bytes())
		}
	}
	// After the undo, searching finds the originals again.
	h.app.runSearch()
	waitSearch(t, h, 3)
}

func TestSearchErrorsAndLiveBuffers(t *testing.T) {
	h, root := searchProject(t)
	h.app.openFile(filepath.Join(root, "b.go"))
	h.app.bufs.Get(filepath.Join(root, "b.go")).ApplyText("package main\n\nvar w = unsaved()\n")
	h.app.openSearch()
	s := &h.app.search
	s.q.Pattern = "unsaved"
	h.app.runSearch()
	waitSearch(t, h, 1)
	s.q.Pattern, s.q.Regex = "call(", true
	h.app.runSearch()
	h.frame()
	if s.err == "" {
		t.Fatal("a bad regex should show an error")
	}
}

func TestManySearchResultsKeepFramesFast(t *testing.T) {
	root := project(t)
	for f := range 40 {
		writeFile(t, filepath.Join(root, "gen", "f"+itoa(f)+".go"), strings.Repeat("var hit = 1\n", 100))
	}
	h := newHarness(t, root, false)
	h.app.openSearch()
	s := &h.app.search
	s.q.Pattern = "hit"
	h.app.runSearch()
	waitSearch(t, h, 4000)
	h.frame()
	// The fastest of a few frames: building every row made each one slow
	// (~90ms), while a busy machine or CI runner only slows some.
	d := time.Hour
	for range 5 {
		t0 := time.Now()
		h.frame()
		d = min(d, time.Since(t0))
	}
	if d > 30*time.Millisecond {
		t.Fatalf("a frame with 4000 results took %v", d)
	}
}

// TestCmdFFindBarKeepsKeys opens the editor's find bar with ⌘F and checks
// that ⌘A and ⌘D typed there act on the find field, not on the document.
func TestCmdFFindBarKeepsKeys(t *testing.T) {
	h, root := searchProject(t)
	h.app.openFile(filepath.Join(root, "a.go"))
	h.frame()
	ed := h.app.curEditor()
	h.press(input.KeyF, input.ModSuper)
	if !ed.FindFocused() || h.app.search.open {
		t.Fatal("⌘F should open the in-file find bar")
	}
	h.typeText("call")
	h.press(input.KeyA, input.ModSuper)
	h.press(input.KeyD, input.ModSuper)
	if ed.Cursors() != 1 || ed.HasSelection() && ed.SelectedText() != "call" {
		t.Fatalf("editor shortcuts reached the document: cursors %d, selection %q", ed.Cursors(), ed.SelectedText())
	}
	if ed.FindQuery() != "call" {
		t.Fatalf("find query %q", ed.FindQuery())
	}
	h.press(input.KeyBackspace, 0) // replaces the ⌘A selection
	if ed.FindQuery() != "" {
		t.Fatalf("⌘A did not select the find field: query %q", ed.FindQuery())
	}
}
