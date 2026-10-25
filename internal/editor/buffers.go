// Package editor owns the one *ui.Editor each open file has. The tab, the
// search dialog's preview and the agent all edit the buffer they get here, so
// an open file never exists twice.
package editor

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/mirzakhany/yoga/ui"
)

// Buffers maps absolute file paths to their editors.
type Buffers struct {
	mu   sync.Mutex
	eds  map[string]*ui.Editor
	opts func() []ui.EditorOption
}

// New returns an empty registry. opts, when set, supplies the options every
// new editor is built with.
func New(opts func() []ui.EditorOption) *Buffers {
	return &Buffers{eds: map[string]*ui.Editor{}, opts: opts}
}

// Get returns the open buffer for path, or nil.
func (b *Buffers) Get(path string) *ui.Editor {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.eds[clean(path)]
}

// Open returns the buffer for path, reading the file the first time. It must
// run on the UI goroutine, since building an editor measures text.
func (b *Buffers) Open(path string) (*ui.Editor, error) {
	path = clean(path)
	b.mu.Lock()
	defer b.mu.Unlock()
	if ed := b.eds[path]; ed != nil {
		return ed, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var opts []ui.EditorOption
	if b.opts != nil {
		opts = b.opts()
	}
	ed := ui.NewEditorFor(path, content, opts...)
	b.eds[path] = ed
	return ed, nil
}

// Save writes the buffer for path to disk and marks it saved.
func (b *Buffers) Save(path string) error {
	ed := b.Get(path)
	if ed == nil {
		return nil
	}
	if err := os.WriteFile(ed.Path, ed.Bytes(), filePerm(ed.Path)); err != nil {
		return err
	}
	ed.MarkSaved()
	return nil
}

// Close releases the buffer for path.
func (b *Buffers) Close(path string) {
	path = clean(path)
	b.mu.Lock()
	ed := b.eds[path]
	delete(b.eds, path)
	b.mu.Unlock()
	if ed != nil {
		ed.Close()
	}
}

// Rename moves an open buffer to a new path after the file was renamed.
func (b *Buffers) Rename(from, to string) {
	from, to = clean(from), clean(to)
	b.mu.Lock()
	defer b.mu.Unlock()
	if ed := b.eds[from]; ed != nil {
		delete(b.eds, from)
		ed.Path = to
		b.eds[to] = ed
	}
}

// Dirty lists the paths of buffers with unsaved changes.
func (b *Buffers) Dirty() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for p, ed := range b.eds {
		if ed.Modified() {
			out = append(out, p)
		}
	}
	return out
}

// CloseAll releases every buffer.
func (b *Buffers) CloseAll() {
	b.mu.Lock()
	eds := b.eds
	b.eds = map[string]*ui.Editor{}
	b.mu.Unlock()
	for _, ed := range eds {
		ed.Close()
	}
}

func clean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// filePerm keeps an existing file's mode; new files get 0644.
func filePerm(path string) os.FileMode {
	if st, err := os.Stat(path); err == nil {
		return st.Mode().Perm()
	}
	return 0o644
}
