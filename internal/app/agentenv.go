// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/agent/tools"
	"github.com/usehm/hm/internal/search"
	"github.com/usehm/hm/internal/workspace"
)

// uiCallTimeout bounds how long the agent waits for the UI goroutine.
const uiCallTimeout = 30 * time.Second

// callUI runs fn on the UI goroutine and waits for it; for the agent's
// goroutine, since editors are not thread-safe.
func (a *App) callUI(fn func()) error {
	done := make(chan struct{})
	a.onUI(func() { fn(); close(done) })
	select {
	case <-done:
		return nil
	case <-time.After(uiCallTimeout):
		return errors.New("the editor did not answer in time")
	}
}

// agentEnv is the workspace for the agent's tools, backed by the editor.
type agentEnv struct{ a *App }

func (e agentEnv) Root() string { return e.a.ws.Root }

// ReadFile returns the open buffer's text, unsaved changes included.
func (e agentEnv) ReadFile(path string) ([]byte, error) {
	var data []byte
	var open bool
	if err := e.a.callUI(func() {
		if ed := e.a.bufs.Get(path); ed != nil {
			data, open = append([]byte(nil), ed.Bytes()...), true
		}
	}); err != nil {
		return nil, err
	}
	if open {
		return data, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a folder; use ls", path)
	}
	return os.ReadFile(path)
}

func (e agentEnv) Search(ctx context.Context, q search.Query, max int) ([]search.FileResult, error) {
	overlay := map[string][]byte{}
	var dirty []string
	_ = e.a.callUI(func() {
		for _, p := range e.a.openBuffers() {
			if ed := e.a.bufs.Get(p); ed != nil {
				overlay[p] = append([]byte(nil), ed.Bytes()...)
				if ed.Modified() {
					dirty = append(dirty, p)
				}
			}
		}
	})
	cfg := e.a.cfg.Current().Search
	var out []search.FileResult
	err := search.Run(ctx, e.Root(), q, search.Options{
		Engine: cfg.Engine, Exclude: cfg.Exclude, MaxResults: max, Dirty: dirty,
		Overlay: func(p string) ([]byte, bool) { b, ok := overlay[p]; return b, ok },
	}, func(fr search.FileResult) bool { out = append(out, fr); return true })
	search.Sort(out)
	return out, err
}

func (e agentEnv) Diagnostics(path string) []string {
	var out []string
	_ = e.a.callUI(func() {
		for _, p := range e.a.problems() {
			if path != "" && p.path != path {
				continue
			}
			sev := "error"
			switch p.diag.Severity {
			case lsp.SeverityWarning:
				sev = "warning"
			case lsp.SeverityInformation, lsp.SeverityHint:
				sev = "info"
			}
			out = append(out, fmt.Sprintf("%s:%d:%d: %s: %s", e.a.ws.Rel(p.path),
				p.diag.Range.Start.Line+1, p.diag.Range.Start.Character+1, sev, p.diag.Message))
		}
	})
	return out
}

// checkpoint is how files were before the agent's current turn touched
// them, for "undo agent turn". A nil entry means the file did not exist.
type checkpoint struct {
	files map[string][]byte
	order []string
}

// applyAgentEdits writes the agent's edits through the buffers, so open tabs
// show them and undo works there too, and saves them so builds and tests
// see them.
func (a *App) applyAgentEdits(r *agentRun, edits []tools.FileEdit) error {
	cp := &r.turnCheckpoint
	if cp.files == nil {
		cp.files = map[string][]byte{}
	}
	for _, e := range edits {
		if _, seen := cp.files[e.Path]; !seen {
			var before []byte
			if ed := a.bufs.Get(e.Path); ed != nil {
				before = append([]byte(nil), ed.Bytes()...)
			} else if data, err := os.ReadFile(e.Path); err == nil {
				before = data
			}
			cp.files[e.Path] = before
			cp.order = append(cp.order, e.Path)
		}
		if err := a.writeAgentFile(e.Path, e.New); err != nil {
			return err
		}
	}
	return nil
}

// writeAgentFile puts content in path; nil content moves it to the trash.
func (a *App) writeAgentFile(path string, content []byte) error {
	if content == nil {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err := workspace.Trash(path); err != nil {
			return err
		}
		a.ws.Removed(path)
		a.bufs.Close(path)
		a.tree.Reload(filepath.Dir(path))
		return nil
	}
	if ed := a.bufs.Get(path); ed != nil {
		ed.ApplyText(string(content))
		return a.bufs.Save(path)
	}
	_, statErr := os.Stat(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	if errors.Is(statErr, fs.ErrNotExist) {
		a.tree.Reload(filepath.Dir(path))
		a.idx.refresh()
	}
	return nil
}

// undoAgentTurn goes back to before the user's last message, which goes
// back in the message box, and puts back every file the last turn
// changed. The undone turn stays in the session as a branch.
func (a *App) undoAgentTurn(r *agentRun) {
	if r.running {
		return
	}
	e, _, ok := r.session.LastPrompt()
	if !ok {
		a.toast("Nothing to undo", ui.ToastInfo)
		return
	}
	cp := r.lastCheckpoint
	var paths, failed []string
	if cp != nil {
		paths = append(paths, cp.order...)
		sort.Strings(paths)
		for _, p := range paths {
			if err := a.writeAgentFile(p, cp.files[p]); err != nil {
				failed = append(failed, a.ws.Rel(p)+": "+err.Error())
			}
		}
	}
	a.editAndResend(r, e)
	if len(failed) > 0 {
		a.toast(fmt.Sprintf("Could not undo %d files: %s", len(failed), failed[0]), ui.ToastError)
		return
	}
	msg := "Went back before your last message; /tree keeps the undone turn"
	if len(paths) > 0 {
		msg += fmt.Sprintf(", and the agent's changes to %d %s are undone", len(paths), plural(len(paths), "file"))
	}
	a.toast(msg, ui.ToastSuccess)
}
