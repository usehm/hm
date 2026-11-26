// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	codeeditor "github.com/yogaui/editor"

	"github.com/yogaui/editor/lsp"
	"github.com/yogaui/yoga/ui"

	"github.com/usehm/hm/internal/lang"
	"github.com/usehm/hm/internal/workspace"
)

// syncFormatTimeout bounds formatting that blocks the UI, before a tab
// closes or a folder switch.
const syncFormatTimeout = 5 * time.Second

// formatRequest builds the formatter run for a buffer, or reports false
// when its language formats nothing on trigger.
func (a *App) formatRequest(ed *codeeditor.Editor, trigger string) (lang.FormatRequest, bool) {
	l, ok := a.lang.For(ed.Path)
	if !ok {
		return lang.FormatRequest{}, false
	}
	req := lang.FormatRequest{Path: ed.Path, Src: ed.Bytes(), Trigger: trigger, Formatters: l.Formatters}
	if len(l.Formatters) == 0 && trigger != "paste" {
		m, path := a.lang.Manager(), ed.Path
		if !m.Supports(path, "documentFormattingProvider") {
			return req, false
		}
		edCfg := a.editorSettings(path)
		opts := lsp.FormattingOptions{TabSize: edCfg.TabWidth, InsertSpaces: edCfg.InsertSpaces}
		req.LSP = func(ctx context.Context, src []byte) ([]byte, error) {
			edits, err := m.Format(ctx, path, opts)
			if err != nil {
				return nil, err
			}
			return lsp.ApplyEdits(src, edits, m.Encoding(path))
		}
	}
	return req, true
}

// format runs the chain for trigger on ed in the background. done, when set,
// runs on the UI goroutine afterwards, whether or not formatting worked.
func (a *App) format(ed *codeeditor.Editor, trigger string, done func()) {
	req, ok := a.formatRequest(ed, trigger)
	if !ok {
		if trigger == "manual" {
			a.toast("No formatter is configured for this file", ui.ToastInfo)
		}
		if done != nil {
			done()
		}
		return
	}
	rev := ed.Revision()
	a.formatting[ed.Path]++
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out, err := lang.Format(ctx, req)
		a.onUI(func() {
			if a.formatting[req.Path]--; a.formatting[req.Path] <= 0 {
				delete(a.formatting, req.Path)
			}
			a.finishFormat(ed, rev, out, err)
			if done != nil {
				done()
			}
		})
	}()
}

// formatNow is format on the UI goroutine, for flows that close the buffer
// right after.
func (a *App) formatNow(ed *codeeditor.Editor, trigger string) {
	req, ok := a.formatRequest(ed, trigger)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncFormatTimeout)
	defer cancel()
	out, err := lang.Format(ctx, req)
	a.finishFormat(ed, ed.Revision(), out, err)
}

// finishFormat applies a formatter's output, unless the buffer changed
// while it ran or the formatter failed.
func (a *App) finishFormat(ed *codeeditor.Editor, rev uint64, out []byte, err error) {
	var missing *lang.MissingError
	switch {
	case errors.Is(err, lang.ErrNothingToRun):
	case errors.As(err, &missing):
		a.lang.Notify(lang.Notice{
			Title: missing.Command + " not installed", Warning: true,
			Message: "Formatting was skipped.",
			Missing: &lang.Missing{Command: missing.Command, Install: missing.Install},
		})
	case err != nil:
		a.appendOutput(err.Error())
		a.notifs.Add("Format failed: "+err.Error(), ui.ToastError, a.openFileAction(ed.Path),
			notifAction{label: "Show Output", run: func() { a.showPanel(panelOutput) }})
		if a.ctx != nil {
			a.ctx.Toasts().Notify(ui.ToastOpts{
				ID: "format-failed", Title: "Format failed", Message: firstLine(err.Error()),
				Variant: ui.ToastError, Duration: 8 * time.Second,
				Actions: []ui.ToastAction{{Label: "Show output", OnClick: func() { a.showPanel(panelOutput) }}},
			})
		}
	case ed.Revision() != rev:
		a.notifs.Add("Formatting skipped: "+a.ws.Rel(ed.Path)+" changed while it ran", ui.ToastInfo)
	default:
		ed.ApplyText(string(out))
	}
}

// saveFile writes path, formatting it first when format_on_save is on.
// Saving a config file applies it.
func (a *App) saveFile(path string) error { return a.saveFileWith(path, true) }

// saveFileWith is saveFile; actions false skips code_actions_on_save, for
// auto save while typing pauses, where organizing imports would drop the
// ones not used yet.
func (a *App) saveFileWith(path string, actions bool) error {
	if workspace.IsUntitled(path) {
		return a.flushUntitled(path) // auto save keeps it for the next run
	}
	ed := a.bufs.Get(path)
	if ed == nil || a.saving[path] {
		return nil // the save in flight writes the file when formatting ends
	}
	if a.editorSettings(path).FormatOnSave && !a.isConfigPath(path) {
		if run, ok := a.saveActions(ed); ok && actions {
			a.formatting[path]++
			a.saving[path] = true
			rev := ed.Revision()
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), saveActionsTimeout)
				defer cancel()
				out, err := run(ctx)
				a.onUI(func() {
					if a.formatting[path]--; a.formatting[path] <= 0 {
						delete(a.formatting, path)
					}
					a.finishSaveActions(ed, rev, out, err)
					if a.bufs.Get(path) != ed {
						delete(a.saving, path)
						return
					}
					a.formatThenWrite(ed)
				})
			}()
			return nil
		}
		if _, ok := a.formatRequest(ed, "save"); ok {
			a.formatThenWrite(ed)
			return nil
		}
	}
	return a.writeFile(path)
}

// formatThenWrite formats ed for a save in the background, then writes it.
func (a *App) formatThenWrite(ed *codeeditor.Editor) {
	path := ed.Path
	a.saving[path] = true
	a.format(ed, "save", func() {
		delete(a.saving, path)
		if a.bufs.Get(path) == ed {
			a.fail("Save failed", a.writeFile(path))
		}
	})
}

// saveFileNow is saveFile that has written the file when it returns.
func (a *App) saveFileNow(path string) error {
	if workspace.IsUntitled(path) {
		return errUntitled
	}
	ed := a.bufs.Get(path)
	if ed == nil {
		return nil
	}
	if a.editorSettings(path).FormatOnSave && !a.isConfigPath(path) {
		if run, ok := a.saveActions(ed); ok {
			ctx, cancel := context.WithTimeout(context.Background(), saveActionsTimeout)
			out, err := run(ctx)
			cancel()
			a.finishSaveActions(ed, ed.Revision(), out, err)
		}
		a.formatNow(ed, "save")
	}
	return a.writeFile(path)
}

// saveActionsTimeout bounds the code actions run before a save, so a busy
// server delays the save only briefly.
const saveActionsTimeout = 3 * time.Second

// saveActions returns a request for the language's code_actions_on_save on
// ed's text, or false when there are none or its server is not ready. The
// request returns the edited text, or nil when nothing changes.
func (a *App) saveActions(ed *codeeditor.Editor) (func(ctx context.Context) ([]byte, error), bool) {
	path := ed.Path
	l, ok := a.lang.For(path)
	if !ok || l.LSP == nil || len(l.LSP.CodeActionsOnSave) == 0 {
		return nil, false
	}
	m, kinds := a.lang.Manager(), l.LSP.CodeActionsOnSave
	if !m.Supports(path, "codeActionProvider") { // also false until the server is ready
		return nil, false
	}
	src, enc := bytes.Clone(ed.Bytes()), m.Encoding(path)
	return func(ctx context.Context) ([]byte, error) {
		whole := lsp.Range{End: lsp.PositionAt(src, len(src), enc)}
		cas, err := m.CodeActionsOf(ctx, path, whole, nil, kinds)
		if err != nil {
			return nil, err
		}
		// The edits are against src, so they are applied together: one
		// action per kind, in the configured order.
		var edits []lsp.TextEdit
		for _, kind := range kinds {
			for _, ca := range cas {
				if ca.Kind != kind && !strings.HasPrefix(ca.Kind, kind+".") {
					continue
				}
				if ca.Edit == nil && len(ca.Data) > 0 {
					if ca, err = m.ResolveCodeAction(ctx, path, ca); err != nil {
						return nil, err
					}
				}
				if ca.Edit != nil {
					edits = append(edits, ca.Edit.Edits()[path]...)
					break
				}
			}
		}
		if len(edits) == 0 {
			return nil, nil
		}
		return lsp.ApplyEdits(src, edits, enc)
	}, true
}

// finishSaveActions applies the save's code actions, unless the buffer
// changed meanwhile; a failure is logged and the save goes on.
func (a *App) finishSaveActions(ed *codeeditor.Editor, rev uint64, out []byte, err error) {
	switch {
	case err != nil:
		a.appendOutput("Code actions on save for " + a.ws.Rel(ed.Path) + " failed: " + err.Error())
	case out == nil || ed.Revision() != rev || bytes.Equal(out, ed.Bytes()):
	default:
		ed.ApplyText(string(out))
	}
}

func (a *App) writeFile(path string) error {
	if ed := a.bufs.Get(path); ed != nil {
		a.trimTrailingWhitespace(ed)
	}
	if err := a.bufs.Save(path); err != nil {
		return err
	}
	if a.isConfigPath(path) {
		a.reloadConfig()
	}
	return nil
}

func (a *App) isConfigPath(path string) bool {
	user, project := a.cfg.Paths()
	return path == user || path == project
}

// formatAfterPaste runs paste formatters once a paste has landed.
func (a *App) formatAfterPaste() {
	p := a.pasted
	if p == nil {
		return
	}
	ed := a.bufs.Get(p.path)
	if ed == nil || ed.Revision() == p.rev {
		if time.Since(p.at) > time.Second {
			a.pasted = nil
		}
		return
	}
	a.pasted = nil
	if req, ok := a.formatRequest(ed, "paste"); ok && len(req.Formatters) > 0 {
		a.format(ed, "paste", nil)
	}
}

type pasteMark struct {
	path string
	rev  uint64
	at   time.Time
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}
