// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"path/filepath"
	"strings"

	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/vim"
)

// vimOn reports whether editors use the Vim keymap (editor.keymap: vim).
func (a *App) vimOn() bool { return a.cfg.Current().Editor.Keymap == "vim" }

// attachVim gives ed a Vim keymap, active while editor.keymap is vim.
func (a *App) attachVim(ed *ui.Editor) {
	st := vim.New(ed, vimHost{a, ed})
	if a.vims == nil {
		a.vims = map[*ui.Editor]*vim.State{}
	}
	a.vims[ed] = st
	ed.KeyFilter = func(ev input.KeyEvent) bool { return a.vimOn() && st.Key(ev) }
	ed.TextFilter = func(r []rune) bool { return a.vimOn() && st.Text(r) }
}

// vimState is the active editor's keymap, when Vim is on.
func (a *App) vimState() *vim.State {
	ed := a.curEditor()
	if ed == nil || !a.vimOn() {
		return nil
	}
	return a.vims[ed]
}

// syncCarets shows a block caret outside insert mode, and drops the
// keymaps of closed editors; call once per frame.
func (a *App) syncCarets() {
	on := a.vimOn()
	for ed, st := range a.vims {
		if a.bufs.Get(ed.Path) != ed {
			delete(a.vims, ed)
			continue
		}
		style := ui.CaretBar
		if on && st.Mode() != vim.Insert {
			style = ui.CaretBlock
		}
		ed.CaretStyle = style
	}
}

// vimHost runs ex commands for one editor.
type vimHost struct {
	a  *App
	ed *ui.Editor
}

func (h vimHost) Ex(cmd string) bool {
	a, path := h.a, h.ed.Path
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return false
	}
	switch strings.TrimSuffix(f[0], "!") {
	case "w":
		a.fail("Save failed", a.saveFileNow(path))
	case "wa":
		a.saveAll(false)
	case "q":
		if strings.HasSuffix(f[0], "!") {
			a.closePath(path)
		} else {
			a.closeTab(a.tabIndex(path))
		}
	case "wq", "x":
		if err := a.saveFileNow(path); err != nil {
			a.fail("Save failed", err)
			return true
		}
		a.closePath(path)
	case "e":
		if len(f) > 1 {
			p := config.ExpandHome(f[1])
			if !filepath.IsAbs(p) {
				p = filepath.Join(a.ws.Root, p)
			}
			a.openFile(p)
		}
	default:
		a.toast("Not an editor command: "+cmd, ui.ToastWarning)
		return false
	}
	return true
}

func (h vimHost) Yank(text string) { h.a.copyText(text) }

// tabIndex is the tab showing path, or -1.
func (a *App) tabIndex(path string) int {
	for i, t := range a.ws.Tabs {
		if t.Path == path {
			return i
		}
	}
	return -1
}
