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
	c.Commands().ToggleChord(a.key("palette"))
	ed := a.ws.ActiveEditor()
	hasEd := ed != nil
	cmds := []*ui.Command{
		ui.Section("File"),
		ui.Cmd("file.quickOpen").Title("Go to File…").Shortcut(a.key("file.quickOpen")).Icon(icons.Search).Run(a.showQuickOpen),
		ui.Cmd("file.openFolder").Title("Open Folder…").Shortcut(a.key("file.openFolder")).Icon(icons.FolderOpen).Run(a.askOpenFolder),
		ui.Cmd("file.newFile").Title("New File…").Shortcut(a.key("file.newFile")).Icon(icons.FilePlus).Run(func() { a.newEntry(a.newFileDir(), false) }),
		ui.Cmd("file.save").Title("Save").Shortcut(a.key("file.save")).Icon(icons.Save).Enabled(hasEd).Run(a.save),
		ui.Cmd("file.saveAll").Title("Save All").Shortcut(a.key("file.saveAll")).Icon(icons.Save).Run(func() { a.saveAll() }),
		ui.Cmd("file.close").Title("Close Tab").Shortcut(a.key("file.close")).Icon(icons.X).Enabled(a.ws.ActiveTab() != nil).Run(func() { a.closeTab(a.ws.Active) }),
		ui.Cmd("file.revealActive").Title("Reveal Active File in Explorer").Icon(icons.Locate).Enabled(hasEd).Run(func() {
			a.sideOpen = true
			a.tree.Reveal(ed.Path)
		}),

		ui.Section("Edit"),
		ui.Cmd("search.project").Title("Search in Project").Shortcut(a.key("search.project")).Icon(icons.Search).Run(a.searchProject),
		ui.Cmd("editor.find").Title("Find in File").Shortcut(a.key("editor.find")).Icon(icons.Search).Enabled(hasEd).Run(func() { ed.OpenFind(false) }),
		ui.Cmd("editor.replace").Title("Replace in File").Shortcut(a.key("editor.replace")).Icon(icons.Replace).Enabled(hasEd).Run(func() { ed.OpenFind(true) }),
		ui.Cmd("editor.undo").Title("Undo").Icon(icons.Undo2).Enabled(hasEd).Hidden(true).Run(func() { ed.Undo() }),
		ui.Cmd("editor.redo").Title("Redo").Icon(icons.Redo2).Enabled(hasEd).Hidden(true).Run(func() { ed.Redo() }),

		ui.Section("View"),
		ui.Cmd("view.sidebar").Title("Toggle Sidebar").Shortcut(a.key("view.sidebar")).Icon(icons.PanelLeft).Run(a.toggleSide),
		ui.Cmd("view.theme").Title("Color Theme…").Shortcut(a.key("view.theme")).Icon(icons.Palette).Run(func() {
			c.Commands().ShowScope(scopeThemes, "Select a color theme")
		}),
		ui.Cmd("view.tabs").Title("Go to Tab…").Shortcut(a.key("view.tabs")).Icon(icons.Layers).Run(func() {
			c.Commands().ShowScope(scopeTabs, "Go to tab")
		}),
		ui.Cmd("panel.terminal").Title("Toggle Terminal").Shortcut(a.key("panel.terminal")).Icon(icons.SquareTerminal).Run(func() { a.togglePanel(panelTerminal) }),
		ui.Cmd("panel.git").Title("Toggle Git").Shortcut(a.key("panel.git")).Icon(icons.GitBranch).Run(func() { a.togglePanel(panelGit) }),
		ui.Cmd("panel.problems").Title("Toggle Problems").Shortcut(a.key("panel.problems")).Icon(icons.TriangleAlert).Run(func() { a.togglePanel(panelProblems) }),
		ui.Cmd("panel.output").Title("Toggle Output").Icon(icons.ScrollText).Run(func() { a.togglePanel(panelOutput) }),
		ui.Cmd("panel.close").Title("Close Panel").Icon(icons.PanelBottomClose).Enabled(a.panelOpen).Run(func() { a.panelOpen = false }),
		ui.Cmd("agent.toggle").Title("Toggle Agent").Shortcut(a.key("agent.toggle")).Icon(icons.Bot).Run(a.toggleAgent),
		ui.Section("Settings"),
		ui.Cmd("settings.yaml").Title("Settings: Open User Config (YAML)").Icon(icons.FileCode).Run(func() { a.openConfigYAML(false) }),
		ui.Cmd("settings.projectYaml").Title("Settings: Open Project Config (YAML)").Icon(icons.FileCode).Run(func() { a.openConfigYAML(true) }),
		ui.Cmd("settings.defaults").Title("Settings: Show Default Config").Icon(icons.FileText).Run(a.openDefaults),
		ui.Cmd("settings.reload").Title("Settings: Reload Config").Icon(icons.RefreshCw).Run(a.reloadConfig),
		ui.Cmd("app.settings").Title("Open Settings").Shortcut(a.key("app.settings")).Icon(icons.Settings).Run(a.openSettings),
	}

	for _, name := range theme.Names() {
		n := name
		cmds = append(cmds, ui.Item("theme."+n).Title(n).Icon(icons.Palette).Scope(scopeThemes).Run(func() { a.setUserSetting([]string{"editor", "theme"}, n) }))
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

// searchProject opens project search. Until the search dialog lands it falls
// back to the active file's find bar, so ⌘F keeps working.
func (a *App) searchProject() {
	if ed := a.ws.ActiveEditor(); ed != nil {
		ed.OpenFind(false)
	}
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
