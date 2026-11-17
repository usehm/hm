// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/shape"

	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/editor"
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

func TestReopenClosedTabs(t *testing.T) {
	w, root := setup(t)
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "sub", "b.go")
	ed, _ := w.Open(a)
	ed.SetSelection(8, 8)
	_, _ = w.Open(b)
	w.Close(0)
	w.Close(0)
	if len(w.Closed) != 2 || w.Closed[1].Path != b {
		t.Fatalf("closed: %+v", w.Closed)
	}

	if p, err := w.Reopen(""); err != nil || p != b {
		t.Fatalf("reopen last: %v %v", p, err)
	}
	if _, err := w.Reopen(a); err != nil {
		t.Fatal(err)
	}
	ed = w.ActiveEditor()
	if l, c := ed.LineCol(ed.Caret()); l != 0 || c != 8 {
		t.Fatalf("caret %d:%d", l, c)
	}
	if _, err := w.Reopen(""); !errors.Is(err, ErrNoClosed) {
		t.Fatalf("nothing left: %v", err)
	}

	// Opening a file normally forgets it; deleted files are not remembered
	// and are skipped when gone.
	w.Close(w.Active)
	_, _ = w.Open(a)
	if len(w.Closed) != 0 {
		t.Fatalf("open should forget: %+v", w.Closed)
	}
	w.Removed(a)
	w.Close(0)
	if len(w.Closed) != 1 || w.Closed[0].Path != b {
		t.Fatalf("removed should not be remembered: %+v", w.Closed)
	}
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Reopen(""); !errors.Is(err, ErrNoClosed) {
		t.Fatalf("gone file: %v", err)
	}
}

func TestClosedTabsInSession(t *testing.T) {
	w, root := setup(t)
	_, _ = w.Open(filepath.Join(root, "a.go"))
	w.Close(0)
	w2 := New(root, editor.New(nil))
	w2.Restore(w.Snapshot())
	if len(w2.Closed) != 1 {
		t.Fatalf("closed: %+v", w2.Closed)
	}
}

func TestImageTabs(t *testing.T) {
	w, root := setup(t)
	img := filepath.Join(root, "logo.png")
	write(t, img, "not really a png")
	w.OpenImage(img)
	w.OpenImage(img)
	if len(w.Tabs) != 1 || w.Tabs[0].Kind != KindImage || w.ActiveEditor() != nil {
		t.Fatalf("tabs: %+v", w.Tabs)
	}
	if w.Buffers.Get(img) != nil {
		t.Fatal("an image should not open a text buffer")
	}
	to := filepath.Join(root, "art", "logo.png")
	w.Renamed(img, to)
	if w.Tabs[0].Path != to {
		t.Fatalf("renamed: %+v", w.Tabs)
	}
	write(t, to, "x")

	w2 := New(root, editor.New(nil))
	w2.Restore(w.Snapshot())
	if len(w2.Tabs) != 1 || w2.Tabs[0].Kind != KindImage {
		t.Fatalf("restored: %+v", w2.Tabs)
	}
	w.Close(0)
	if p, err := w.Reopen(""); err != nil || p != to || w.Tabs[0].Kind != KindImage {
		t.Fatalf("reopen: %q %v %+v", p, err, w.Tabs)
	}
	w.Removed(filepath.Join(root, "art"))
	if len(w.Tabs) != 0 {
		t.Fatalf("removed: %+v", w.Tabs)
	}
}
