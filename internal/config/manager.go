package config

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Manager holds the live config: the last good merge of the defaults, the
// user file and the project file, plus what is wrong with them now.
type Manager struct {
	mu       sync.Mutex
	user     string
	project  string
	cfg      Config
	good     bool
	problems []Problem

	watcher *fsnotify.Watcher
	timer   *time.Timer
	pending bool
	wake    func()
}

// NewManager loads config from the user and project files. project may be
// empty when no folder is open.
func NewManager(user, project string) *Manager {
	m := &Manager{user: user, project: project}
	m.Reload()
	return m
}

// Paths returns the user and project file paths.
func (m *Manager) Paths() (user, project string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.user, m.project
}

// Reload reads the files again. With problems, the last good config stays
// (on the first load, the layers with problems are left out instead) and
// applied is false.
func (m *Manager) Reload() (applied bool, problems []Problem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var layers []Layer
	for _, p := range []string{m.user, m.project} {
		if p == "" {
			continue
		}
		l, err := ReadLayer(p)
		if err != nil {
			problems = append(problems, Problem{File: p, Msg: err.Error()})
			continue
		}
		layers = append(layers, l)
	}
	cfg, ps := Load(layers...)
	problems = append(problems, ps...)
	m.problems = problems
	if len(problems) > 0 && m.good {
		return false, problems
	}
	// First load with problems: run on the layers that parsed.
	m.cfg, m.good = cfg, true
	return len(problems) == 0, problems
}

// Current returns the live config.
func (m *Manager) Current() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// Problems returns what the last Reload found wrong.
func (m *Manager) Problems() []Problem {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Problem(nil), m.problems...)
}

// SetProject switches the project file, as when another folder opens, and
// reloads.
func (m *Manager) SetProject(path string) (bool, []Problem) {
	m.mu.Lock()
	m.project = path
	m.mu.Unlock()
	m.rewatch()
	return m.Reload()
}

// Watch starts watching both files. wake runs, from the watcher's goroutine,
// after a change settles; the UI then calls TakeChange and Reload.
func (m *Manager) Watch(wake func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.watcher, m.wake = w, wake
	m.mu.Unlock()
	m.rewatch()
	go m.loop(w)
	return nil
}

// rewatch watches the folders holding the files, so a file that does not
// exist yet is noticed when it is created. The project root is watched too,
// for the .hm folder appearing.
func (m *Manager) rewatch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.watcher == nil {
		return
	}
	for _, p := range m.watcher.WatchList() {
		_ = m.watcher.Remove(p)
	}
	for _, f := range []string{m.user, m.project} {
		if f == "" {
			continue
		}
		dir := filepath.Dir(f)
		if f == m.user {
			_ = os.MkdirAll(dir, 0o755)
		}
		_ = m.watcher.Add(dir)
		if f == m.project {
			_ = m.watcher.Add(filepath.Dir(dir))
		}
	}
}

func (m *Manager) loop(w *fsnotify.Watcher) {
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			m.event(ev)
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
		}
	}
}

// settle is how long the watcher waits for a burst of events (an editor's
// write-then-rename) to end.
const settle = 60 * time.Millisecond

func (m *Manager) event(ev fsnotify.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := filepath.Clean(ev.Name)
	relevant := name == m.user || name == m.project
	if m.project != "" && name == filepath.Dir(m.project) {
		relevant = true // .hm created or removed
		if ev.Has(fsnotify.Create) && m.watcher != nil {
			_ = m.watcher.Add(name)
		}
	}
	if !relevant {
		return
	}
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(settle, func() {
		m.mu.Lock()
		m.pending = true
		wake := m.wake
		m.mu.Unlock()
		if wake != nil {
			wake()
		}
	})
}

// TakeChange reports, once, that a watched file changed.
func (m *Manager) TakeChange() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.pending
	m.pending = false
	return p
}

// Close stops watching.
func (m *Manager) Close() {
	m.mu.Lock()
	w := m.watcher
	m.watcher = nil
	if m.timer != nil {
		m.timer.Stop()
	}
	m.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}
