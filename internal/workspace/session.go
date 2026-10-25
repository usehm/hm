package workspace

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Session is what hm remembers between runs: app state, not configuration.
type Session struct {
	Root   string       `json:"root"`
	Tabs   []SessionTab `json:"tabs"`
	Active int          `json:"active"`
	Side   *bool        `json:"side,omitempty"`
}

// SessionTab is one tab with the caret it had.
type SessionTab struct {
	Kind   Kind    `json:"kind,omitempty"`
	Path   string  `json:"path,omitempty"`
	Line   int     `json:"line"`
	Col    int     `json:"col"`
	Scroll float32 `json:"scroll,omitempty"`
}

// StateDir is where hm keeps app state. Tests point it elsewhere.
var StateDir = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "hm")
}

func sessionPath() string { return filepath.Join(StateDir(), "state.json") }

// LoadSession reads the last saved session; a missing file is an empty one.
func LoadSession() (Session, error) {
	s := Session{Active: -1}
	data, err := os.ReadFile(sessionPath())
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// SaveSession writes s atomically.
func SaveSession(s Session) error {
	path := sessionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Snapshot captures the workspace's tabs and carets.
func (w *Workspace) Snapshot() Session {
	s := Session{Root: w.Root, Active: w.Active}
	for _, t := range w.Tabs {
		st := SessionTab{Kind: t.Kind, Path: t.Path}
		if ed := w.Buffers.Get(t.Path); t.Kind == KindFile && ed != nil {
			st.Line, st.Col = ed.LineCol(ed.Caret())
			st.Scroll = ed.ScrollPx
		}
		s.Tabs = append(s.Tabs, st)
	}
	return s
}

// Restore reopens the session's tabs, skipping files that are gone. It returns
// the paths that could not be opened.
func (w *Workspace) Restore(s Session) []string {
	var missing []string
	active := -1
	for i, st := range s.Tabs {
		if st.Kind != KindFile {
			w.OpenPage(st.Kind)
		} else {
			ed, err := w.Open(st.Path)
			if err != nil {
				missing = append(missing, st.Path)
				continue
			}
			off := ed.Offset(st.Line, st.Col)
			ed.SetSelection(off, off)
			ed.ScrollPx = st.Scroll
		}
		if i == s.Active {
			active = len(w.Tabs) - 1
		}
	}
	if active >= 0 {
		w.Active = active
	} else if len(w.Tabs) > 0 {
		w.Active = 0
	}
	return missing
}
