// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Package workspace holds the open folder and the tab strip: which files are
// open, in what order, which one is active, and which have unsaved changes.
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"slices"

	codeeditor "github.com/yogaui/editor"

	"github.com/usehm/hm/internal/editor"
)

// Kind tells file tabs apart from app pages shown as tabs.
type Kind int

const (
	KindFile Kind = iota
	KindSettings
	KindGit
	KindImage // an image file, shown rather than edited
)

// HasPath reports whether tabs of kind k show a file.
func (k Kind) HasPath() bool { return k == KindFile || k == KindImage }

// Tab is one entry in the tab strip.
type Tab struct {
	Kind Kind
	Path string // absolute; empty for non-file tabs
}

// Workspace is the open folder and its tabs.
type Workspace struct {
	Root    string
	Tabs    []Tab
	Active  int
	Buffers *editor.Buffers
	// Closed are recently closed file tabs, the most recent last.
	Closed []SessionTab
}

// maxClosed is how many closed tabs Reopen remembers.
const maxClosed = 20

// New returns a workspace rooted at root that opens files through bufs.
func New(root string, bufs *editor.Buffers) *Workspace {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Workspace{Root: root, Buffers: bufs, Active: -1}
}

// Open opens path in a tab, or activates the tab that already shows it.
func (w *Workspace) Open(path string) (*codeeditor.Editor, error) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if i := w.indexOf(KindFile, path); i >= 0 {
		w.Active = i
		return w.Buffers.Get(path), nil
	}
	ed, err := w.Buffers.Open(path)
	if err != nil {
		return nil, err
	}
	w.forget(path)
	w.Tabs = append(w.Tabs, Tab{Kind: KindFile, Path: path})
	w.Active = len(w.Tabs) - 1
	return ed, nil
}

// OpenImage opens the image at path in a tab, or activates the tab that
// already shows it.
func (w *Workspace) OpenImage(path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if i := w.indexOf(KindImage, path); i >= 0 {
		w.Active = i
		return
	}
	w.forget(path)
	w.Tabs = append(w.Tabs, Tab{Kind: KindImage, Path: path})
	w.Active = len(w.Tabs) - 1
}

// OpenPage shows a non-file page such as Settings as a tab.
func (w *Workspace) OpenPage(k Kind) {
	if i := w.indexOf(k, ""); i >= 0 {
		w.Active = i
		return
	}
	w.Tabs = append(w.Tabs, Tab{Kind: k})
	w.Active = len(w.Tabs) - 1
}

func (w *Workspace) indexOf(k Kind, path string) int {
	for i, t := range w.Tabs {
		if t.Kind == k && t.Path == path {
			return i
		}
	}
	return -1
}

// ActiveTab returns the active tab, or nil when none is open.
func (w *Workspace) ActiveTab() *Tab {
	if w.Active < 0 || w.Active >= len(w.Tabs) {
		return nil
	}
	return &w.Tabs[w.Active]
}

// ActiveEditor returns the active file tab's editor, or nil.
func (w *Workspace) ActiveEditor() *codeeditor.Editor {
	t := w.ActiveTab()
	if t == nil || t.Kind != KindFile {
		return nil
	}
	return w.Buffers.Get(t.Path)
}

// Modified reports whether tab i has unsaved changes. An untitled buffer
// with text has them until it is saved to a file of its own.
func (w *Workspace) Modified(i int) bool {
	if i < 0 || i >= len(w.Tabs) || w.Tabs[i].Kind != KindFile {
		return false
	}
	path := w.Tabs[i].Path
	ed := w.Buffers.Get(path)
	return ed != nil && (ed.Modified() || IsUntitled(path) && len(ed.Bytes()) > 0)
}

// HasDirty reports whether any tab has unsaved changes.
func (w *Workspace) HasDirty() bool {
	for i := range w.Tabs {
		if w.Modified(i) {
			return true
		}
	}
	return false
}

// SaveActive writes the active file tab.
func (w *Workspace) SaveActive() error {
	t := w.ActiveTab()
	if t == nil || t.Kind != KindFile {
		return errors.New("nothing to save")
	}
	return w.Buffers.Save(t.Path)
}

// SaveAll writes every modified file tab, returning the first error.
func (w *Workspace) SaveAll() error {
	var first error
	for i, t := range w.Tabs {
		if w.Modified(i) {
			if err := w.Buffers.Save(t.Path); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// Close removes tab i without asking; callers confirm unsaved changes first.
// A file tab is remembered for Reopen; an untitled buffer is deleted.
func (w *Workspace) Close(i int) {
	if i < 0 || i >= len(w.Tabs) {
		return
	}
	if t := w.Tabs[i]; IsUntitled(t.Path) {
		w.Drop(i)
		_ = os.Remove(t.Path)
		return
	} else if t.Kind.HasPath() {
		st := SessionTab{Kind: t.Kind, Path: t.Path}
		if ed := w.Buffers.Get(t.Path); ed != nil {
			st.Line, st.Col = ed.LineCol(ed.Caret())
			st.Scroll = ed.ScrollPx
		}
		w.forget(t.Path)
		w.Closed = append(w.Closed, st)
		if n := len(w.Closed) - maxClosed; n > 0 {
			w.Closed = w.Closed[n:]
		}
	}
	w.Drop(i)
}

// Drop removes tab i without asking or remembering it, such as for a file
// that was deleted or moved to another group.
func (w *Workspace) Drop(i int) {
	if i < 0 || i >= len(w.Tabs) {
		return
	}
	if t := w.Tabs[i]; t.Kind == KindFile {
		w.Buffers.Close(t.Path)
	}
	w.Tabs = append(w.Tabs[:i], w.Tabs[i+1:]...)
	switch {
	case len(w.Tabs) == 0:
		w.Active = -1
	case w.Active > i || w.Active >= len(w.Tabs):
		w.Active--
	}
}

// forget drops path from the closed tabs.
func (w *Workspace) forget(path string) {
	w.Closed = slices.DeleteFunc(w.Closed, func(st SessionTab) bool { return st.Path == path })
}

// Reopen opens the closed tab for path, or the most recently closed one for
// "", with the caret it had, and returns its path. Closed files that are gone
// are skipped.
func (w *Workspace) Reopen(path string) (string, error) {
	for i := len(w.Closed) - 1; i >= 0; i-- {
		st := w.Closed[i]
		if path != "" && st.Path != path {
			continue
		}
		w.Closed = slices.Delete(w.Closed, i, i+1)
		if _, err := os.Stat(st.Path); err != nil {
			if path != "" {
				return "", err
			}
			continue
		}
		return st.Path, w.reopen(st)
	}
	return "", ErrNoClosed
}

// reopen opens st's file with the caret and scroll it had.
func (w *Workspace) reopen(st SessionTab) error {
	if st.Kind == KindImage {
		w.OpenImage(st.Path)
		return nil
	}
	ed, err := w.Open(st.Path)
	if err != nil {
		return err
	}
	off := ed.Offset(st.Line, st.Col)
	ed.SetSelection(off, off)
	ed.ScrollPx = st.Scroll
	return nil
}

// ErrNoClosed is Reopen's error when there is nothing to reopen.
var ErrNoClosed = errors.New("no closed tab to reopen")

// CloseAll removes every tab.
func (w *Workspace) CloseAll() {
	for len(w.Tabs) > 0 {
		w.Close(len(w.Tabs) - 1)
	}
}

// Rel returns path relative to the workspace root when it is inside it.
func (w *Workspace) Rel(path string) string {
	if rel, err := filepath.Rel(w.Root, path); err == nil && !filepath.IsAbs(rel) && rel != ".." && !startsDotDot(rel) {
		return rel
	}
	return path
}

func startsDotDot(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}

// Renamed updates tabs after from was renamed to to. from may be a folder.
func (w *Workspace) Renamed(from, to string) {
	for i, t := range w.Tabs {
		if !t.Kind.HasPath() {
			continue
		}
		np := ""
		if t.Path == from {
			np = to
		} else if rest, ok := under(t.Path, from); ok {
			np = filepath.Join(to, rest)
		}
		if np == "" {
			continue
		}
		if t.Kind == KindFile {
			w.Buffers.Rename(t.Path, np)
		}
		w.Tabs[i].Path = np
	}
}

// Removed closes tabs for path, or for files under it when it is a folder.
func (w *Workspace) Removed(path string) {
	for i := len(w.Tabs) - 1; i >= 0; i-- {
		t := w.Tabs[i]
		if !t.Kind.HasPath() {
			continue
		}
		if _, ok := under(t.Path, path); ok || t.Path == path {
			w.Drop(i)
		}
	}
}

// under reports whether path is inside dir, returning the part after it.
func under(path, dir string) (string, bool) {
	prefix := dir + string(filepath.Separator)
	if len(path) > len(prefix) && path[:len(prefix)] == prefix {
		return path[len(prefix):], true
	}
	return "", false
}
