package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// maxWatchedDirs caps the folders watched. kqueue (macOS, BSD) holds a file
// descriptor per watched entry, so a huge tree would run out of them; past
// the cap, changes deeper down are picked up by refreshes instead.
const maxWatchedDirs = 1500

// watchSettle coalesces a burst of events (a git checkout, a build).
const watchSettle = 120 * time.Millisecond

// Changes is what changed on disk since the last Take.
type Changes struct {
	Dirs  map[string]bool // folders whose entries were added, removed or renamed
	Files map[string]bool // files written
	Git   bool            // something under .git that moves HEAD or the index
}

// Empty reports whether nothing changed.
func (c Changes) Empty() bool { return len(c.Dirs) == 0 && len(c.Files) == 0 && !c.Git }

// Watcher reports file changes under a workspace root.
type Watcher struct {
	root string
	skip func(name string) bool
	wake func()
	w    *fsnotify.Watcher

	mu      sync.Mutex
	pending Changes
	ready   bool
	timer   *time.Timer
	dirs    int
}

// Watch starts watching root. skip names folders not to descend into.
func Watch(root string, skip func(name string) bool, wake func()) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{root: root, skip: skip, wake: wake, w: fw, pending: newChanges()}
	w.addTree(root)
	_ = fw.Add(filepath.Join(root, ".git"))
	go w.loop()
	return w, nil
}

func newChanges() Changes { return Changes{Dirs: map[string]bool{}, Files: map[string]bool{}} }

// addTree watches dir and the folders below it, up to the cap.
func (w *Watcher) addTree(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != dir && (d.Name() == ".git" || w.skip(d.Name())) {
			return filepath.SkipDir
		}
		w.mu.Lock()
		full := w.dirs >= maxWatchedDirs
		if !full {
			w.dirs++
		}
		w.mu.Unlock()
		if full {
			return filepath.SkipAll
		}
		_ = w.w.Add(p)
		return nil
	})
}

func (w *Watcher) loop() {
	for {
		select {
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}
			w.event(ev)
		case _, ok := <-w.w.Errors:
			if !ok {
				return
			}
		}
	}
}

func (w *Watcher) event(ev fsnotify.Event) {
	name := filepath.Clean(ev.Name)
	gitDir := filepath.Join(w.root, ".git")
	w.mu.Lock()
	switch {
	case filepath.Dir(name) == gitDir:
		base := filepath.Base(name)
		if base == "HEAD" || base == "index" || base == "MERGE_HEAD" || strings.HasPrefix(base, "ORIG_HEAD") {
			w.pending.Git = true
		} else {
			w.mu.Unlock()
			return
		}
	case ev.Has(fsnotify.Create) || ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
		w.pending.Dirs[filepath.Dir(name)] = true
		w.pending.Files[name] = true
		if ev.Has(fsnotify.Create) {
			if st, err := os.Stat(name); err == nil && st.IsDir() && !w.skip(st.Name()) && st.Name() != ".git" {
				w.mu.Unlock()
				w.addTree(name)
				w.mu.Lock()
			}
		}
	case ev.Has(fsnotify.Write):
		w.pending.Files[name] = true
	default:
		w.mu.Unlock()
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(watchSettle, func() {
		w.mu.Lock()
		w.ready = true
		w.mu.Unlock()
		if w.wake != nil {
			w.wake()
		}
	})
	w.mu.Unlock()
}

// Take returns the changes once they settled; call it from the UI loop.
func (w *Watcher) Take() Changes {
	if w == nil {
		return Changes{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ready {
		return Changes{}
	}
	c := w.pending
	w.pending, w.ready = newChanges(), false
	return c
}

// Close stops watching.
func (w *Watcher) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	_ = w.w.Close()
}
