package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/workspace"
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
	a.treeQuery = ""
	a.idx = newFileIndex(a.ws.Root, a.invalidate)
	if a.cfg != nil {
		if p := config.ProjectPath(a.ws.Root); func() bool { _, proj := a.cfg.Paths(); return proj != p }() {
			a.cfg.SetProject(p)
		}
		a.applyConfig(a.cfg.Current())
	}
}

// saveFile writes path. Saving a config file applies it right away.
func (a *App) saveFile(path string) error {
	if err := a.bufs.Save(path); err != nil {
		return err
	}
	if user, project := a.cfg.Paths(); path == user || path == project {
		a.reloadConfig()
	}
	return nil
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

// openFile opens path in a tab and shows it in the tree.
func (a *App) openFile(path string) {
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
				if err := a.saveFile(path); err != nil {
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
				if a.saveAll() {
					fn()
				}
			}},
		},
	})
}

func (a *App) save() {
	t := a.ws.ActiveTab()
	if t == nil || t.Kind != workspace.KindFile {
		return
	}
	a.fail("Save failed", a.saveFile(t.Path))
}

// saveAll writes every modified tab and reports whether all succeeded.
func (a *App) saveAll() bool {
	ok := true
	for i, t := range a.ws.Tabs {
		if a.ws.Modified(i) {
			if err := a.saveFile(t.Path); err != nil {
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
			ui.MenuItem{Label: "Rename…", OnSelect: func() { a.renameEntry(path) }},
			ui.MenuItem{Label: "Move to Trash", OnSelect: func() { a.trashEntry(path) }},
		)
	}
	items = append(items,
		ui.MenuItem{Label: "Copy Path", OnSelect: func() { a.copyText(path) }},
		ui.MenuItem{Label: "Copy Relative Path", OnSelect: func() { a.copyText(a.ws.Rel(path)) }},
		ui.MenuItem{Label: revealLabel(), OnSelect: func() { a.fail("Reveal failed", reveal(path)) }},
	)
	return items
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
	a.ctx.Dialogs().ShowInput("Rename "+filepath.Base(path), filepath.Base(path), func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || name == filepath.Base(path) {
			return
		}
		dst := filepath.Join(filepath.Dir(path), name)
		if _, err := os.Stat(dst); err == nil {
			a.fail("Rename failed", fmt.Errorf("%s already exists", name))
			return
		}
		if err := os.Rename(path, dst); err != nil {
			a.fail("Rename failed", err)
			return
		}
		a.ws.Renamed(path, dst)
		a.tree.Reload(filepath.Dir(path))
		a.idx.refresh()
	}, nil)
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

func reveal(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer", "/select,", path).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}
