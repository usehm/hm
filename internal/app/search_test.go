package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
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

func TestCmdFOpensSearchAndRegexGroupsReplace(t *testing.T) {
	h, root := searchProject(t)
	h.app.openFile(filepath.Join(root, "main.go"))
	h.frame()
	h.press(input.KeyF, input.ModSuper)
	if !h.app.search.open || !h.c.Dialogs().Open {
		t.Fatal("⌘F should open the search dialog")
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
	t0 := time.Now()
	h.frame()
	d := time.Since(t0)
	if d > 30*time.Millisecond { // building every row took ~90ms
		t.Fatalf("a frame with 4000 results took %v", d)
	}
}
