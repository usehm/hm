// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/workspace"
)

// hiddenNames are left out of the tree and the file index.
var hiddenNames = map[string]bool{".git": true, ".DS_Store": true, "node_modules": true}

func hideEntry(name string, _ bool) bool { return hiddenNames[name] }

// openFolder makes root the workspace. Callers confirm unsaved changes first.
func (a *App) openFolder(root string) {
	if a.ws != nil {
		a.ws.CloseAll()
	}
	a.idx.stop()
	a.ws = workspace.New(root, a.bufs)
	a.tree = ui.NewFileTree(a.ws.Root)
	a.tree.Hidden = hideEntry
	a.tree.Reload(a.ws.Root)
	a.tree.OnOpenFile = func(path string) {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			a.openFile(path)
		}
	}
	a.tree.OnMove = func(src, dst string) { a.ws.Renamed(src, dst); a.idx.refresh() }
	a.tree.SetContextMenu(a.treeMenu)
	a.tree.OnKey = a.treeKey
	a.fileClip = fileClip{}
	a.tree.SetLabelColor(a.treeLabelColor)
	a.tree.SetIconFor(treeIcon)
	a.treeQuery = ""
	a.idx = newFileIndex(a.ws.Root, a.invalidate)
	a.startGit()
	if a.cfg != nil {
		if p := config.ProjectPath(a.ws.Root); func() bool { _, proj := a.cfg.Paths(); return proj != p }() {
			a.cfg.SetProject(p)
		}
		a.applyConfig(a.cfg.Current())
		a.initAgent()
	}
}

// askOpenFolder picks a folder and opens it, confirming unsaved changes.
func (a *App) askOpenFolder() {
	a.ctx.Files().Show(ui.FileDialogOpts{
		Title: "Open Folder",
		Mode:  ui.FileDialogOpenFolder,
		Dir:   a.ws.Root,
		OnConfirm: func(paths []string) {
			if len(paths) == 0 {
				return
			}
			a.confirmDiscard("Open folder?", func() { a.openFolder(paths[0]) })
		},
	})
}

// openFile opens path in a tab of the focused group and shows it in the
// tree.
func (a *App) openFile(path string) {
	if g := a.focusedGroup(); g != nil {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		a.groupOpen(g, path)
		a.tree.Reveal(path)
		return
	}
	if _, err := a.ws.Open(path); err != nil {
		a.fail("Open failed", err)
		return
	}
	a.tree.Reveal(path)
}

// closeTab closes tab i, asking to save it when it has unsaved changes.
func (a *App) closeTab(i int) {
	if !a.ws.Modified(i) {
		a.ws.Close(i)
		return
	}
	name := filepath.Base(a.ws.Tabs[i].Path)
	path := a.ws.Tabs[i].Path
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title:      "Save changes to " + name + "?",
		Width:      420,
		FitContent: true,
		Severity:   ui.DialogSeverityWarning,
		Body: func(*ui.Ctx) ui.View {
			return ui.Paragraph("Your changes will be lost if you don't save them.")
		},
		Actions: []ui.DialogAction{
			{Label: "Cancel"},
			{Label: "Don't Save", OnClick: func() { a.closePath(path) }},
			{Label: "Save", Primary: true, OnClick: func() {
				if err := a.saveFileNow(path); err != nil {
					a.fail("Save failed", err)
					return
				}
				a.closePath(path)
			}},
		},
	})
}

// closePath closes the tab showing path; indexes shift while dialogs are open.
func (a *App) closePath(path string) {
	for i, t := range a.ws.Tabs {
		if t.Kind == workspace.KindFile && t.Path == path {
			a.ws.Close(i)
			return
		}
	}
}

// confirmDiscard runs fn, first asking when any tab has unsaved changes.
func (a *App) confirmDiscard(title string, fn func()) {
	if !a.ws.HasDirty() {
		fn()
		return
	}
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title:      title,
		Width:      440,
		FitContent: true,
		Severity:   ui.DialogSeverityWarning,
		Body: func(*ui.Ctx) ui.View {
			return ui.Paragraph("Some files have unsaved changes.")
		},
		Actions: []ui.DialogAction{
			{Label: "Cancel"},
			{Label: "Discard", OnClick: fn},
			{Label: "Save All", Primary: true, OnClick: func() {
				if a.saveAll(true) {
					fn()
				}
			}},
		},
	})
}

func (a *App) save() {
	if g := a.focusedGroup(); g != nil {
		a.syncMirrors()
		if p := a.curPath(); p != "" {
			a.fail("Save failed", a.saveFile(p))
		}
		return
	}
	t := a.ws.ActiveTab()
	if t == nil || t.Kind != workspace.KindFile {
		return
	}
	a.fail("Save failed", a.saveFile(t.Path))
}

// saveAll writes every modified tab and reports whether all succeeded. With
// now set, every file is written before it returns.
func (a *App) saveAll(now bool) bool {
	ok := true
	save := a.saveFile
	if now {
		save = a.saveFileNow
	}
	for i, t := range a.ws.Tabs {
		if a.ws.Modified(i) {
			if err := save(t.Path); err != nil {
				a.fail("Save failed", err)
				ok = false
			}
		}
	}
	return ok
}

// treeMenu is the file tree's right-click menu for path.
func (a *App) treeMenu(path string) []ui.MenuItem {
	if path == "" {
		path = a.ws.Root
	}
	dir := path
	isDir := true
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		dir = filepath.Dir(path)
		isDir = false
	}
	items := []ui.MenuItem{}
	if !isDir {
		items = append(items, ui.MenuItem{Label: "Open", OnSelect: func() { a.openFile(path) }})
	}
	items = append(items,
		ui.MenuItem{Label: "New File…", OnSelect: func() { a.newEntry(dir, false) }},
		ui.MenuItem{Label: "New Folder…", OnSelect: func() { a.newEntry(dir, true) }},
	)
	if path != a.ws.Root {
		items = append(items,
			ui.MenuItem{Label: "Cut", OnSelect: func() { a.clipFiles(path, true) }},
			ui.MenuItem{Label: "Copy", OnSelect: func() { a.clipFiles(path, false) }},
			ui.MenuItem{Label: "Rename…", OnSelect: func() { a.renameEntry(path) }},
			ui.MenuItem{Label: "Move to Trash", OnSelect: func() { a.trashEntry(path) }},
		)
	}
	items = append(items,
		ui.MenuItem{Label: "Paste", Disabled: len(a.fileClip.paths) == 0, OnSelect: func() { a.pasteFiles(path) }},
		ui.MenuItem{Label: "Copy Path", OnSelect: func() { a.copyText(path) }},
		ui.MenuItem{Label: "Copy Relative Path", OnSelect: func() { a.copyText(a.ws.Rel(path)) }},
		ui.MenuItem{Label: revealLabel(), OnSelect: func() { a.fail("Reveal failed", reveal(path)) }},
	)
	return items
}

// fileClip holds files copied or cut in the tree until they are pasted.
type fileClip struct {
	paths []string
	cut   bool
}

func (a *App) clipFiles(path string, cut bool) {
	a.fileClip = fileClip{paths: []string{path}, cut: cut}
	a.copyText(path) // other apps get the path as text
}

// pasteFiles copies or moves the clipped files into path, or into its
// folder when path is a file.
func (a *App) pasteFiles(path string) {
	if len(a.fileClip.paths) == 0 {
		return
	}
	dir := path
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		dir = filepath.Dir(path)
	}
	var last string
	for _, src := range a.fileClip.paths {
		var dst string
		var err error
		if a.fileClip.cut {
			dst, err = workspace.MoveInto(src, dir)
		} else {
			dst, err = workspace.CopyInto(src, dir)
		}
		if err != nil {
			a.fail("Paste failed", err)
			continue
		}
		if a.fileClip.cut && dst != src {
			a.ws.Renamed(src, dst)
			a.tree.Reload(filepath.Dir(src))
		}
		last = dst
	}
	if a.fileClip.cut {
		a.fileClip = fileClip{} // a cut pastes once
	}
	a.tree.Reload(dir)
	a.idx.refresh()
	if last != "" {
		a.tree.Reveal(last)
	}
}

// treeKey handles copy, cut and paste of files while the tree has focus.
func (a *App) treeKey(path string, ev input.KeyEvent) bool {
	if !ev.Mods.Primary() || ev.Mods.Has(input.ModShift) || ev.Mods.Has(input.ModAlt) {
		return false
	}
	switch ev.Key {
	case input.KeyC, input.KeyX:
		if path == "" || path == a.ws.Root {
			return false
		}
		a.clipFiles(path, ev.Key == input.KeyX)
	case input.KeyV:
		if path == "" {
			path = a.ws.Root
		}
		a.pasteFiles(path)
	default:
		return false
	}
	return true
}

func (a *App) copyText(s string) {
	if clip := a.ctx.Clipboard(); clip != nil {
		clip.Set(s)
	}
}

func (a *App) newEntry(dir string, folder bool) {
	title := "New file name"
	if folder {
		title = "New folder name"
	}
	a.ctx.Dialogs().ShowInput(title, "name", func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			a.fail("Create failed", fmt.Errorf("%s already exists", name))
			return
		}
		var err error
		if folder {
			err = os.MkdirAll(p, 0o755)
		} else {
			if err = os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
				err = os.WriteFile(p, nil, 0o644)
			}
		}
		if err != nil {
			a.fail("Create failed", err)
			return
		}
		a.tree.Reload(dir)
		a.idx.refresh()
		if !folder {
			a.openFile(p)
		}
	}, nil)
}

func (a *App) renameEntry(path string) {
	base := filepath.Base(path)
	dir := filepath.Dir(path)
	// Select the name without its extension, the part usually renamed.
	stem := len(base)
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		if n := len(base) - len(filepath.Ext(base)); n > 0 {
			stem = n
		}
	}
	a.ctx.Dialogs().ShowInputOpts(ui.InputOpts{
		Title:     "Rename " + base,
		Value:     base,
		SelectEnd: stem,
		Validate:  func(name string) string { return renameProblem(path, strings.TrimSpace(name)) },
		OnOK: func(name string) {
			name = strings.TrimSpace(name)
			if name == "" || name == base {
				return
			}
			dst := filepath.Join(dir, name)
			if msg := renameProblem(path, name); msg != "" {
				a.fail("Rename failed", errors.New(msg))
				return
			}
			if err := os.Rename(path, dst); err != nil {
				a.fail("Rename failed", err)
				return
			}
			a.ws.Renamed(path, dst)
			a.tree.Reload(dir)
			a.idx.refresh()
		},
	})
}

// renameProblem says why path cannot be renamed to name, or "" when it can.
// Keeping the name is fine; OK then does nothing.
func renameProblem(path, name string) string {
	switch {
	case name == "":
		return "A name is required"
	case name == "." || name == ".." || strings.ContainsAny(name, `/\`):
		return "Not a valid file name"
	case name == filepath.Base(path):
		return ""
	}
	dst := filepath.Join(filepath.Dir(path), name)
	st, err := os.Stat(dst)
	if err != nil {
		return ""
	}
	// A case-only rename on a case-insensitive disk finds the file itself.
	if src, err := os.Stat(path); err == nil && os.SameFile(src, st) {
		return ""
	}
	return name + " already exists"
}

func (a *App) trashEntry(path string) {
	a.ctx.Dialogs().ShowAction("Move "+filepath.Base(path)+" to the Trash?", a.ws.Rel(path), func() {
		if err := workspace.Trash(path); err != nil {
			a.fail("Delete failed", err)
			return
		}
		a.ws.Removed(path)
		a.tree.Reload(filepath.Dir(path))
		a.idx.refresh()
	}, nil)
}

func revealLabel() string {
	switch runtime.GOOS {
	case "darwin":
		return "Reveal in Finder"
	case "windows":
		return "Reveal in Explorer"
	}
	return "Open Containing Folder"
}

// openURL opens url in the default browser.
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func reveal(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer", "/select,", path).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}
