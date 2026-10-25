package app

import (
	"path/filepath"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"
)

// Scopes keep long lists out of the main palette.
const (
	scopeFiles  = "files"
	scopeThemes = "themes"
	scopeTabs   = "tabs"
)

// registerCommands lists every action. Menus, buttons and shortcuts all go
// through these ids.
func (a *App) registerCommands(c *ui.Ctx) {
	c.Commands().ToggleChord("⌘⇧P")
	ed := a.ws.ActiveEditor()
	hasEd := ed != nil
	cmds := []*ui.Command{
		ui.Section("File"),
		ui.Cmd("file.quickOpen").Title("Go to File…").Shortcut("⌘P").Icon(icons.Search).Run(a.showQuickOpen),
		ui.Cmd("file.openFolder").Title("Open Folder…").Shortcut("⌘O").Icon(icons.FolderOpen).Run(a.askOpenFolder),
		ui.Cmd("file.newFile").Title("New File…").Shortcut("⌘N").Icon(icons.FilePlus).Run(func() { a.newEntry(a.newFileDir(), false) }),
		ui.Cmd("file.save").Title("Save").Shortcut("⌘S").Icon(icons.Save).Enabled(hasEd).Run(a.save),
		ui.Cmd("file.saveAll").Title("Save All").Shortcut("⌘⌥S").Icon(icons.Save).Run(a.saveAll),
		ui.Cmd("file.close").Title("Close Tab").Shortcut("⌘W").Icon(icons.X).Enabled(a.ws.ActiveTab() != nil).Run(func() { a.closeTab(a.ws.Active) }),
		ui.Cmd("file.revealActive").Title("Reveal Active File in Explorer").Icon(icons.Locate).Enabled(hasEd).Run(func() {
			a.sideOpen = true
			a.tree.Reveal(ed.Path)
		}),

		ui.Section("Edit"),
		ui.Cmd("editor.undo").Title("Undo").Icon(icons.Undo2).Enabled(hasEd).Hidden(true).Run(func() { ed.Undo() }),
		ui.Cmd("editor.redo").Title("Redo").Icon(icons.Redo2).Enabled(hasEd).Hidden(true).Run(func() { ed.Redo() }),

		ui.Section("View"),
		ui.Cmd("view.sidebar").Title("Toggle Sidebar").Shortcut("⌘B").Icon(icons.PanelLeft).Run(a.toggleSide),
		ui.Cmd("view.theme").Title("Color Theme…").Shortcut("⌘K").Icon(icons.Palette).Run(func() {
			c.Commands().ShowScope(scopeThemes, "Select a color theme")
		}),
		ui.Cmd("view.tabs").Title("Go to Tab…").Shortcut("⌘⇧E").Icon(icons.Layers).Run(func() {
			c.Commands().ShowScope(scopeTabs, "Go to tab")
		}),
		ui.Cmd("panel.terminal").Title("Toggle Terminal").Shortcut("Ctrl+`").Icon(icons.SquareTerminal).Run(func() { a.togglePanel(panelTerminal) }),
		ui.Cmd("panel.git").Title("Toggle Git").Shortcut("⌘⇧G").Icon(icons.GitBranch).Run(func() { a.togglePanel(panelGit) }),
		ui.Cmd("panel.problems").Title("Toggle Problems").Shortcut("⌘⇧M").Icon(icons.TriangleAlert).Run(func() { a.togglePanel(panelProblems) }),
		ui.Cmd("panel.output").Title("Toggle Output").Icon(icons.ScrollText).Run(func() { a.togglePanel(panelOutput) }),
		ui.Cmd("panel.close").Title("Close Panel").Icon(icons.PanelBottomClose).Enabled(a.panelOpen).Run(func() { a.panelOpen = false }),
		ui.Cmd("agent.toggle").Title("Toggle Agent").Shortcut("⌘L").Icon(icons.Bot).Run(a.toggleAgent),
		ui.Cmd("app.settings").Title("Open Settings").Shortcut("⌘,").Icon(icons.Settings).Run(a.openSettings),
	}

	for _, name := range theme.Names() {
		n := name
		cmds = append(cmds, ui.Item("theme."+n).Title(n).Icon(icons.Palette).Scope(scopeThemes).Run(func() { theme.Use(n) }))
	}
	for i, t := range a.ws.Tabs {
		i := i
		m := a.tabModel(t, i)
		cmds = append(cmds, ui.Item("tab."+itoa(i)+"."+t.Path).Title(m.Title).Detail(a.ws.Rel(t.Path)).
			Icon(m.Icon).Scope(scopeTabs).Run(func() { a.ws.Active = i }))
	}
	if a.idx != nil {
		for _, p := range a.idx.files {
			p := p
			rel := a.ws.Rel(p)
			cmds = append(cmds, ui.Item("open."+p).Title(filepath.Base(p)).Detail(filepath.Dir(rel)).
				Icon(icons.File).Scope(scopeFiles).Run(func() { a.openFile(p) }))
		}
	}
	c.Commands().Register(cmds...)
}

func (a *App) showQuickOpen() {
	a.ctx.Commands().ShowScope(scopeFiles, "Search files by name")
}

// newFileDir is the active file's folder, or the workspace root.
func (a *App) newFileDir() string {
	if ed := a.ws.ActiveEditor(); ed != nil {
		return filepath.Dir(ed.Path)
	}
	return a.ws.Root
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}
