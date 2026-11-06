package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/shape"

	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/editor"
)

func setup(t *testing.T) (*Workspace, string) {
	t.Helper()
	text, err := shape.NewEngine(1, false)
	if err != nil {
		t.Skip(err)
	}
	yoga.SetResources(text, render.NewSpriteSheet(text.Atlas), &input.MemClipboard{})
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package a\n")
	write(t, filepath.Join(root, "sub", "b.go"), "package b\n")
	config.StateDir = func() string { return filepath.Join(root, ".state") }
	return New(root, editor.New(nil)), root
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenEditSaveClose(t *testing.T) {
	w, root := setup(t)
	a := filepath.Join(root, "a.go")
	ed, err := w.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := w.Open(a)
	if again != ed || len(w.Tabs) != 1 {
		t.Fatal("opening an open file must reuse its buffer and tab")
	}
	if w.HasDirty() {
		t.Fatal("fresh file is not dirty")
	}
	ed.SetText("package a\n\nvar x = 1\n")
	if !w.Modified(0) || !w.HasDirty() {
		t.Fatal("edit should mark the tab dirty")
	}
	if err := w.SaveActive(); err != nil {
		t.Fatal(err)
	}
	if w.HasDirty() {
		t.Fatal("save should clear dirty")
	}
	got, _ := os.ReadFile(a)
	if string(got) != "package a\n\nvar x = 1\n" {
		t.Fatalf("file content %q", got)
	}
	w.Close(0)
	if len(w.Tabs) != 0 || w.Active != -1 || w.Buffers.Get(a) != nil {
		t.Fatal("close should drop tab and buffer")
	}
}

func TestCloseKeepsActiveSensible(t *testing.T) {
	w, root := setup(t)
	_, _ = w.Open(filepath.Join(root, "a.go"))
	_, _ = w.Open(filepath.Join(root, "sub", "b.go"))
	w.OpenPage(KindSettings)
	w.Active = 2
	w.Close(0)
	if w.Active != 1 || w.ActiveTab().Kind != KindSettings {
		t.Fatalf("active after closing an earlier tab: %d", w.Active)
	}
	w.Close(1)
	if w.Active != 0 {
		t.Fatalf("active after closing the last tab: %d", w.Active)
	}
}

func TestRenameAndRemoveFolder(t *testing.T) {
	w, root := setup(t)
	b := filepath.Join(root, "sub", "b.go")
	_, _ = w.Open(b)
	nsub := filepath.Join(root, "pkg")
	if err := os.Rename(filepath.Join(root, "sub"), nsub); err != nil {
		t.Fatal(err)
	}
	w.Renamed(filepath.Join(root, "sub"), nsub)
	nb := filepath.Join(nsub, "b.go")
	if w.Tabs[0].Path != nb || w.Buffers.Get(nb) == nil || w.Buffers.Get(nb).Path != nb {
		t.Fatalf("tab after folder rename: %+v", w.Tabs[0])
	}
	w.Removed(nsub)
	if len(w.Tabs) != 0 {
		t.Fatal("removing a folder closes its files' tabs")
	}
}

func TestSessionRoundTrip(t *testing.T) {
	w, root := setup(t)
	ed, _ := w.Open(filepath.Join(root, "a.go"))
	ed.SetSelection(8, 8) // line 0, col 8
	_, _ = w.Open(filepath.Join(root, "sub", "b.go"))
	w.OpenPage(KindSettings)
	w.Active = 0
	if err := SaveSession(w.Snapshot()); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "sub", "b.go")); err != nil {
		t.Fatal(err)
	}

	w2 := New(root, editor.New(nil))
	missing := w2.Restore(s)
	if len(missing) != 1 {
		t.Fatalf("missing: %v", missing)
	}
	if len(w2.Tabs) != 2 || w2.Tabs[1].Kind != KindSettings {
		t.Fatalf("tabs: %+v", w2.Tabs)
	}
	if w2.Active != 0 {
		t.Fatalf("active %d", w2.Active)
	}
	if l, c := w2.ActiveEditor().LineCol(w2.ActiveEditor().Caret()); l != 0 || c != 8 {
		t.Fatalf("caret %d:%d", l, c)
	}
}

func TestTrashMovesFile(t *testing.T) {
	if os.Getenv("HM_TEST_TRASH") == "" {
		t.Skip("set HM_TEST_TRASH=1 to touch the real trash")
	}
	_, root := setup(t)
	p := filepath.Join(root, "a.go")
	if err := Trash(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("file still there")
	}
}
