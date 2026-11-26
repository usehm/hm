// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/yogaui/yoga/icons"
	"github.com/yogaui/yoga/ui"

	"github.com/usehm/hm/internal/workspace"
)

// errUntitled stops a save that would need a file name first, such as
// Save All before closing tabs.
var errUntitled = errors.New("untitled files need Save As first")

// newUntitled opens an empty buffer for pasting into, with no file yet.
func (a *App) newUntitled() {
	a.focusGroup = 0
	if _, err := a.ws.NewUntitled(); err != nil {
		a.fail("Could not create an untitled file", err)
	}
}

// flushUntitled writes an untitled buffer to its scratch file, so it
// survives a restart. It does not count as a save.
func (a *App) flushUntitled(path string) error {
	ed := a.bufs.Get(path)
	if ed == nil {
		return nil
	}
	if rev, ok := a.untitledRevs[path]; ok && rev == ed.Revision() {
		return nil
	}
	if err := os.WriteFile(path, ed.Bytes(), 0o644); err != nil {
		return err
	}
	a.untitledRevs[path] = ed.Revision()
	return nil
}

// flushAllUntitled writes every untitled buffer, before the session is saved.
func (a *App) flushAllUntitled() {
	for _, t := range a.ws.Tabs {
		if workspace.IsUntitled(t.Path) {
			_ = a.flushUntitled(t.Path)
		}
	}
}

// saveUntitledAs asks where to save an untitled buffer, writes it there and
// moves its tab to the new file. then, when set, runs with the new path.
func (a *App) saveUntitledAs(path string, then func(string)) {
	ed := a.bufs.Get(path)
	if ed == nil {
		return
	}
	a.ctx.Files().Show(ui.FileDialogOpts{
		Title:             "Save " + workspace.UntitledName(path),
		Mode:              ui.FileDialogSaveFile,
		Dir:               a.ws.Root,
		AllowCreateFolder: true,
		OnConfirm: func(paths []string) {
			if len(paths) == 0 || a.bufs.Get(path) != ed {
				return
			}
			to, err := a.saveUntitledTo(path, paths[0])
			if err != nil {
				a.fail("Save failed", err)
				return
			}
			if then != nil {
				then(to)
			}
		},
	})
}

// saveUntitledTo writes an untitled buffer to to, moves its tab there and
// removes the scratch file. A name without an extension keeps the language
// the buffer was given.
func (a *App) saveUntitledTo(path, to string) (string, error) {
	ed := a.bufs.Get(path)
	if ed == nil {
		return "", os.ErrNotExist
	}
	if filepath.Ext(to) == "" {
		to += filepath.Ext(path)
	}
	if err := os.WriteFile(to, ed.Bytes(), 0o644); err != nil {
		return "", err
	}
	a.ws.Renamed(path, to)
	ed.MarkSaved()
	_ = os.Remove(path)
	delete(a.untitledRevs, path)
	a.tree.Reload(filepath.Dir(to))
	a.tree.Reveal(to)
	return to, nil
}

// closeUntitled closes an untitled tab, asking first when it has text.
func (a *App) closeUntitled(path string) {
	ed := a.bufs.Get(path)
	if ed == nil || len(ed.Bytes()) == 0 {
		a.closePath(path)
		return
	}
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title:      "Save " + workspace.UntitledName(path) + "?",
		Width:      420,
		FitContent: true,
		Severity:   ui.DialogSeverityWarning,
		Body: func(*ui.Ctx) ui.View {
			return ui.Paragraph("Its text will be lost if you don't save it.")
		},
		Actions: []ui.DialogAction{
			{Label: "Cancel"},
			{Label: "Don't Save", OnClick: func() { a.closePath(path) }},
			{Label: "Save As…", Primary: true, OnClick: func() { a.saveUntitledAs(path, a.closePath) }},
		},
	})
}

// changeLanguage lists the languages for an untitled buffer; picking one
// gives its file that language's extension, so highlighting, the language
// server and formatters follow.
func (a *App) changeLanguage(path string) {
	var items []*ui.Command
	cur := filepath.Ext(path)
	for _, l := range a.lang.Languages() {
		ext := l.Extensions[0]
		check := ""
		if strings.EqualFold(ext, cur) {
			check = "current"
		}
		items = append(items, ui.Item("lang."+l.Name).Title(l.Name).Detail(ext).Trailing(check).
			Icon(icons.FileCode).Scope(scopeLocations).
			Run(func() { a.setUntitledLanguage(path, ext) }))
	}
	a.locations = items
	a.ctx.Commands().Register(items...)
	a.ctx.Commands().ShowScope(scopeLocations, "Language of "+workspace.UntitledName(path))
}

// setUntitledLanguage renames an untitled buffer's file to end in ext.
func (a *App) setUntitledLanguage(path, ext string) {
	to := filepath.Join(filepath.Dir(path), workspace.UntitledName(path)+ext)
	if to == path {
		return
	}
	if err := a.flushUntitled(path); err != nil {
		a.fail("Could not change the language", err)
		return
	}
	if err := os.Rename(path, to); err != nil {
		a.fail("Could not change the language", err)
		return
	}
	a.ws.Renamed(path, to)
	a.untitledRevs[to] = a.untitledRevs[path]
	delete(a.untitledRevs, path)
}
