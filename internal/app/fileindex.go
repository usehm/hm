// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"io/fs"
	"path/filepath"
	"sort"
	"sync"

	"github.com/mirzakhany/yoga/ui"
)

// maxIndexed caps the quick-open list so huge trees stay responsive.
const maxIndexed = 50000

// fileIndex lists the workspace's files in the background for quick open.
type fileIndex struct {
	root string
	wake func()

	mu      sync.Mutex
	pending []string
	ready   bool
	gen     int

	files []string // UI goroutine only
	// cmds are the quick-open entries for files, built once per walk
	// rather than every frame. UI goroutine only.
	cmds []*ui.Command
}

func newFileIndex(root string, wake func()) *fileIndex {
	x := &fileIndex{root: root, wake: wake}
	x.refresh()
	return x
}

// refresh re-walks the tree; a walk in flight is superseded.
func (x *fileIndex) refresh() {
	if x == nil {
		return
	}
	x.mu.Lock()
	x.gen++
	gen := x.gen
	x.mu.Unlock()
	go x.walk(gen)
}

func (x *fileIndex) walk(gen int) {
	var out []string
	_ = filepath.WalkDir(x.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != x.root && (hiddenNames[d.Name()] || skipDirs[d.Name()]) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			out = append(out, p)
			if len(out) >= maxIndexed {
				return filepath.SkipAll
			}
		}
		return nil
	})
	sort.Strings(out)
	x.mu.Lock()
	if gen == x.gen {
		x.pending, x.ready = out, true
	}
	x.mu.Unlock()
	if x.wake != nil {
		x.wake()
	}
}

// skipDirs are build and dependency folders not worth listing.
var skipDirs = map[string]bool{"vendor": true, "dist": true, "build": true, ".hm-cache": true}

// drain takes a finished walk's results; call at the top of Body.
func (x *fileIndex) drain() {
	if x == nil {
		return
	}
	x.mu.Lock()
	if x.ready {
		x.files, x.pending, x.ready = x.pending, nil, false
		x.cmds = nil
	}
	x.mu.Unlock()
}

// stop drops results of walks still running.
func (x *fileIndex) stop() {
	if x == nil {
		return
	}
	x.mu.Lock()
	x.gen++
	x.mu.Unlock()
}
