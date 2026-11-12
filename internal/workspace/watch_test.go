// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package workspace

import (
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// git reading the index makes kqueue report a chmod on it; that must not
// count as a change, or each status read would schedule another.
func TestWatchIgnoresChmod(t *testing.T) {
	root := t.TempDir()
	w := &Watcher{root: root, skip: func(string) bool { return false }, pending: newChanges()}
	index := filepath.Join(root, ".git", "index")
	file := filepath.Join(root, "a.go")

	w.event(fsnotify.Event{Name: index, Op: fsnotify.Chmod})
	w.event(fsnotify.Event{Name: file, Op: fsnotify.Chmod})
	if !w.pending.Empty() || w.timer != nil {
		t.Fatalf("chmod counted as a change: %+v", w.pending)
	}

	w.event(fsnotify.Event{Name: index, Op: fsnotify.Write | fsnotify.Chmod})
	w.timer.Stop()
	if !w.pending.Git {
		t.Fatal("a write to the index was not reported")
	}
}
