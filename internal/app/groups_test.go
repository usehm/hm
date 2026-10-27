package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitViewMirrorsAFile(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, true)
	main := filepath.Join(root, "main.go")
	h.app.openFile(main)
	h.frame()
	h.app.splitRight()
	h.frame()
	if len(h.app.groups) != 1 || h.app.focusGroup != 1 {
		t.Fatalf("groups %d focus %d", len(h.app.groups), h.app.focusGroup)
	}
	m := h.app.curEditor()
	base := h.app.bufs.Get(main)
	if m == base || string(m.Bytes()) != string(base.Bytes()) {
		t.Fatal("the split should show its own editor with the same text")
	}

	// An edit in the split reaches the file's buffer, and back.
	m.SetSelection(0, 0)
	m.HandleText([]rune("// split\n"))
	h.frame()
	if !strings.HasPrefix(string(base.Bytes()), "// split\n") || !base.Modified() {
		t.Fatalf("buffer after split edit: %q", base.Bytes()[:20])
	}
	base.SetSelection(0, 0)
	base.HandleText([]rune("// base\n"))
	h.frame()
	if !strings.HasPrefix(string(m.Bytes()), "// base\n// split\n") {
		t.Fatalf("split after buffer edit: %q", m.Bytes()[:30])
	}

	// Opening a file while the split has focus opens it there.
	h.app.openFile(filepath.Join(root, "go.mod"))
	h.frame()
	if g := h.app.groups[0]; len(g.paths) != 2 || len(h.app.ws.Tabs) != 1 {
		t.Fatalf("group paths %v, first group tabs %d", g.paths, len(h.app.ws.Tabs))
	}

	// Saving from the split writes the file.
	h.app.groups[0].active = 0
	h.app.save()
	h.until("saved from the split", 5*time.Second, func() bool { return !base.Modified() })

	h.app.Close()
	h2 := newHarness(t, root, true)
	if len(h2.app.groups) != 1 || len(h2.app.groups[0].paths) != 2 {
		t.Fatalf("groups not restored: %d", len(h2.app.groups))
	}
	h2.app.closeGroup(h2.app.groups[0])
	if len(h2.app.groups) != 0 {
		t.Fatal("close group")
	}
}
