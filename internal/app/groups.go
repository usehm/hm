package app

import (
	"path/filepath"

	"github.com/mirzakhany/yoga/highlight"
	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"
)

// Editor groups: the workspace's tabs are the first group; split view adds
// more, side by side. A file in another group shows in a mirror: an editor
// of its own (its own caret, scroll and undo) whose text is kept equal to
// the file's buffer, which stays the one the language server, formatters,
// search and saving use.

// editorGroup is a group beyond the first: file tabs and their mirrors.
type editorGroup struct {
	paths   []string
	active  int
	mirrors map[string]*mirror
}

// mirror is a group's editor for a file, and the revisions of it and of
// the file's buffer when they were last made equal.
type mirror struct {
	ed                *ui.Editor
	seenOwn, seenBase uint64
}

// maxGroups is how many groups split view goes to, the first included.
const maxGroups = 4

// focusedGroup is the extra group with focus, or nil for the first.
func (a *App) focusedGroup() *editorGroup {
	if a.focusGroup <= 0 || a.focusGroup > len(a.groups) {
		return nil
	}
	return a.groups[a.focusGroup-1]
}

// curEditor is the focused group's active editor: the one commands act on.
func (a *App) curEditor() *ui.Editor {
	if g := a.focusedGroup(); g != nil {
		if m := g.activeMirror(); m != nil {
			return m.ed
		}
	}
	return a.ws.ActiveEditor()
}

// curPath is the file of curEditor, or "".
func (a *App) curPath() string {
	if g := a.focusedGroup(); g != nil && g.active < len(g.paths) {
		return g.paths[g.active]
	}
	if t := a.ws.ActiveTab(); t != nil {
		return t.Path
	}
	return ""
}

// editorFor is the editor showing path in the focused group.
func (a *App) editorFor(path string) *ui.Editor {
	if g := a.focusedGroup(); g != nil {
		if m := g.mirrors[path]; m != nil {
			return m.ed
		}
	}
	return a.bufs.Get(path)
}

// lspTarget is the buffer language features run on for curEditor: a
// mirror's file buffer, with the mirror's selection.
func (a *App) lspTarget() *ui.Editor {
	g := a.focusedGroup()
	if g == nil {
		return a.ws.ActiveEditor()
	}
	m := g.activeMirror()
	if m == nil {
		return nil
	}
	a.syncMirrors()
	base := a.bufs.Get(g.paths[g.active])
	if base != nil {
		lo, hi := m.ed.Selection()
		if m.ed.Caret() == lo {
			lo, hi = hi, lo
		}
		base.SetSelection(lo, hi)
	}
	return base
}

func (g *editorGroup) activeMirror() *mirror {
	if g.active < 0 || g.active >= len(g.paths) {
		return nil
	}
	return g.mirrors[g.paths[g.active]]
}

// splitRight shows the current file in a new group to the right.
func (a *App) splitRight() {
	path := a.curPath()
	if path == "" || len(a.groups)+1 >= maxGroups {
		return
	}
	g := &editorGroup{mirrors: map[string]*mirror{}}
	a.groups = append(a.groups, g)
	a.focusGroup = len(a.groups)
	a.groupOpen(g, path)
}

// moveToOtherGroup moves the current file to the next group, making one
// when there is none.
func (a *App) moveToOtherGroup() {
	path := a.curPath()
	if path == "" {
		return
	}
	from := a.focusGroup
	if len(a.groups) == 0 || from == len(a.groups) {
		if from == 0 {
			a.splitRight()
			a.ws.Close(a.ws.Active)
			return
		}
		a.focusGroup = 0
		a.openFile(path)
		a.groupClose(a.groups[from-1], path)
		return
	}
	a.focusGroup = from + 1
	a.groupOpen(a.groups[from], path)
	if from == 0 {
		a.closeTab(a.ws.Active)
	} else {
		a.groupClose(a.groups[from-1], path)
	}
}

// groupOpen shows path in g, making its mirror.
func (a *App) groupOpen(g *editorGroup, path string) {
	base, err := a.bufs.Open(path)
	if err != nil {
		a.fail("Open failed", err)
		return
	}
	for i, p := range g.paths {
		if p == path {
			g.active = i
			return
		}
	}
	ed := ui.NewEditor(base.Bytes(), highlight.ForPath(path), editorOptions()...)
	off := base.Caret()
	ed.SetSelection(off, off)
	ed.ScrollPx = base.ScrollPx
	a.attachVim(ed)
	g.mirrors[path] = &mirror{ed: ed, seenOwn: ed.Revision(), seenBase: base.Revision()}
	g.paths = append(g.paths, path)
	g.active = len(g.paths) - 1
	ed.Focus()
}

// groupClose closes path's tab in g, and g when it was its last.
func (a *App) groupClose(g *editorGroup, path string) {
	a.syncMirrors() // keep its last edits in the file's buffer
	for i, p := range g.paths {
		if p != path {
			continue
		}
		if m := g.mirrors[p]; m != nil {
			delete(a.vims, m.ed)
			m.ed.Close()
		}
		delete(g.mirrors, p)
		g.paths = append(g.paths[:i], g.paths[i+1:]...)
		if g.active >= len(g.paths) {
			g.active = len(g.paths) - 1
		}
		break
	}
	if len(g.paths) == 0 {
		a.closeGroup(g)
	}
	a.keepBuffer(path)
}

// closeGroup removes g and its tabs.
func (a *App) closeGroup(g *editorGroup) {
	for i, x := range a.groups {
		if x != g {
			continue
		}
		a.syncMirrors() // keep their last edits in the files' buffers
		a.groups = append(a.groups[:i], a.groups[i+1:]...)
		if a.focusGroup > len(a.groups) || a.focusGroup == i+1 {
			a.focusGroup = 0
		}
		for _, p := range g.paths {
			if m := g.mirrors[p]; m != nil {
				delete(a.vims, m.ed)
				m.ed.Close()
			}
			a.keepBuffer(p)
		}
		g.paths, g.mirrors = nil, nil
		return
	}
}

// keepBuffer closes a file's buffer once no tab or group shows it, unless it
// has unsaved changes.
func (a *App) keepBuffer(path string) {
	if a.hasTab(path) || a.inGroup(path) {
		return
	}
	if ed := a.bufs.Get(path); ed != nil && !ed.Modified() && !a.search.borrowed[path] {
		a.bufs.Close(path)
	}
}

func (a *App) inGroup(path string) bool {
	for _, g := range a.groups {
		if g.mirrors[path] != nil {
			return true
		}
	}
	return false
}

// syncMirrors makes each mirror and its file's buffer equal again: the one
// edited since they last were wins. Call once per frame and before saving.
func (a *App) syncMirrors() {
	for _, g := range a.groups {
		for path, m := range g.mirrors {
			base := a.bufs.Get(path)
			if base == nil {
				var err error
				if base, err = a.bufs.Open(path); err != nil {
					continue
				}
				m.seenBase = ^uint64(0) // a fresh buffer: the mirror wins
			}
			switch {
			case m.ed.Revision() != m.seenOwn:
				base.ApplyText(string(m.ed.Bytes()))
			case base.Revision() != m.seenBase:
				m.ed.ApplyText(string(base.Bytes()))
				if !base.Modified() {
					m.ed.MarkSaved()
				}
			}
			m.seenOwn, m.seenBase = m.ed.Revision(), base.Revision()
			m.ed.SetDiagnostics(base.Diagnostics())
		}
	}
	// The focused group follows keyboard focus.
	if ed := a.ws.ActiveEditor(); ed != nil && ed.Focused() {
		a.focusGroup = 0
	}
	for i, g := range a.groups {
		if m := g.activeMirror(); m != nil && m.ed.Focused() {
			a.focusGroup = i + 1
		}
	}
}

// groupView is an extra group: its tabs, breadcrumb and editor.
func (a *App) groupView(c *ui.Ctx, gi int, g *editorGroup) ui.View {
	tabs := make([]ui.TabModel, len(g.paths))
	for i, p := range g.paths {
		base := a.bufs.Get(p)
		tabs[i] = ui.TabModel{Title: filepath.Base(p), Icon: icons.File, Modified: base != nil && base.Modified()}
	}
	id := "group-" + itoa(gi)
	view := ui.View(ui.Spacer())
	if m := g.activeMirror(); m != nil {
		view = ui.Column(a.breadcrumb(c, g.paths[g.active]), ui.ViewOf(m.ed).Grow(1)).Grow(1)
	}
	return ui.Column(
		ui.Tabs(id+"-tabs", tabs).
			Selected(g.active).
			OnSelectItem(func(i int, _ string) { g.active = i; a.focusGroup = gi + 1 }).
			OnTabClose(func(i int) { a.groupClose(g, g.paths[i]) }).
			OnTabContextMenu(func(i int) []ui.MenuItem {
				p := g.paths[i]
				return []ui.MenuItem{
					{Label: "Close", OnSelect: func() { a.groupClose(g, p) }},
					{Label: "Close Group", OnSelect: func() { a.closeGroup(g) }},
					{Label: "Move to First Group", OnSelect: func() {
						a.focusGroup = 0
						a.openFile(p)
						a.groupClose(g, p)
					}},
				}
			}),
		view,
	).Grow(1).BorderLeft(ui.TokenBorder, c.Theme().Stroke.Thin)
}

// withGroups lays the first group out beside the others.
func (a *App) withGroups(c *ui.Ctx, first ui.View) ui.View {
	if len(a.groups) == 0 {
		return first
	}
	panes := []ui.View{first}
	for i, g := range a.groups {
		panes = append(panes, a.groupView(c, i, g))
	}
	pct := make([]float32, len(panes))
	for i := range pct {
		pct[i] = 100 / float32(len(panes))
	}
	return ui.Splitter("editor-groups-"+itoa(len(panes)), ui.Horizontal, panes...).Percents(pct...).Grow(1)
}

// focusNextGroup moves focus to the next group, round to the first.
func (a *App) focusNextGroup() {
	a.focusGroup = (a.focusGroup + 1) % (len(a.groups) + 1)
	if ed := a.curEditor(); ed != nil {
		ed.Focus()
	}
}
