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
	// scopeLocations lists references, definitions and symbols.
	scopeLocations = "locations"
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
		ui.Cmd("file.saveAll").Title("Save All").Shortcut(a.key("file.saveAll")).Icon(icons.Save).Run(func() { a.saveAll(false) }),
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

		ui.Section("Code"),
		ui.Cmd("editor.definition").Title("Go to Definition").Shortcut(a.key("editor.definition")).Icon(icons.SquareArrowOutUpRight).
			Enabled(hasEd).Run(func() { a.definition(ed, ed.Caret()) }),
		ui.Cmd("editor.implementation").Title("Go to Implementation").Shortcut(a.key("editor.implementation")).Icon(icons.SquareArrowOutUpRight).
			Enabled(hasEd).Run(func() { a.implementation(ed, ed.Caret()) }),
		ui.Cmd("editor.references").Title("Find References").Shortcut(a.key("editor.references")).Icon(icons.ListTree).
			Enabled(hasEd).Run(func() { a.references(ed, ed.Caret()) }),
		ui.Cmd("editor.rename").Title("Rename Symbol").Shortcut(a.key("editor.rename")).Icon(icons.PencilLine).
			Enabled(hasEd).Run(func() { a.rename(ed) }),
		ui.Cmd("editor.symbols").Title("Go to Symbol in File…").Shortcut(a.key("editor.symbols")).Icon(icons.Hash).
			Enabled(hasEd).Run(func() { a.documentSymbols(ed) }),
		ui.Cmd("editor.format").Title("Format Document").Shortcut(a.key("editor.format")).Icon(icons.WandSparkles).
			Enabled(hasEd).Run(func() { a.format(ed, "manual", nil) }),
		ui.Cmd("completion.trigger").Title("Suggest AI Completion").Shortcut(a.key("completion.trigger")).Icon(icons.Sparkles).
			Enabled(hasEd).Run(a.triggerCompletion),
		ui.Cmd("completion.toggle").Title("Toggle AI Completion").Icon(icons.Sparkles).Run(func() {
			a.setUserSetting([]string{"completion", "enabled"}, !a.cfg.Current().Completion.Enabled)
		}),
		ui.Cmd("lsp.restart").Title("Restart Language Servers").Icon(icons.RefreshCw).Run(func() {
			a.lang.Restart()
			a.toast("Language servers restarted", ui.ToastInfo)
		}),

		ui.Section("Git"),
		ui.Cmd("git.refresh").Title("Git: Refresh").Icon(icons.RefreshCw).Run(func() { a.gitst.page.Refresh() }),
		ui.Cmd("git.pull").Title("Git: Pull").Icon(icons.ArrowDown).Run(func() { a.gitst.page.Pull() }),
		ui.Cmd("git.push").Title("Git: Push").Icon(icons.ArrowUp).Run(func() { a.gitst.page.Push() }),
		ui.Cmd("git.fetch").Title("Git: Fetch").Icon(icons.RefreshCw).Run(func() { a.gitst.page.Fetch() }),
		ui.Cmd("git.newBranch").Title("Git: New Branch…").Icon(icons.GitBranchPlus).Run(func() { a.gitst.page.NewBranch() }),
		ui.Cmd("app.checkUpdates").Title("Check for Updates").Icon(icons.Download).Run(a.checkUpdatesNow),
		ui.Cmd("app.installCLI").Title("Install 'hm' Command in PATH").Icon(icons.SquareTerminal).Hidden(!cliSupported()).Run(a.installCLICommand),
		ui.Cmd("app.uninstallCLI").Title("Uninstall 'hm' Command from PATH").Icon(icons.SquareTerminal).Hidden(!cliSupported()).Run(a.uninstallCLICommand),

		ui.Section("View"),
		ui.Cmd("view.sidebar").Title("Toggle Sidebar").Shortcut(a.key("view.sidebar")).Icon(icons.PanelLeft).Run(a.toggleSide),
		ui.Cmd("view.theme").Title("Color Theme…").Shortcut(a.key("view.theme")).Icon(icons.Palette).Run(func() {
			c.Commands().ShowScope(scopeThemes, "Select a color theme")
		}),
		ui.Cmd("markdown.preview").Title("Markdown: Toggle Preview").Shortcut(a.key("markdown.preview")).Icon(icons.Eye).
			Enabled(hasEd && isMarkdown(ed.Path)).Run(a.cycleMarkdownPreview),
		ui.Cmd("view.tabs").Title("Go to Tab…").Shortcut(a.key("view.tabs")).Icon(icons.Layers).Run(func() {
			c.Commands().ShowScope(scopeTabs, "Go to tab")
		}),
		ui.Cmd("panel.terminal").Title("Toggle Terminal").Shortcut(a.key("panel.terminal")).Icon(icons.SquareTerminal).Run(func() { a.togglePanel(panelTerminal) }),
		ui.Cmd("terminal.new").Title("Terminal: New").Shortcut(a.key("terminal.new")).Icon(icons.SquareTerminal).Run(a.newTerminal),
		ui.Cmd("terminal.kill").Title("Terminal: Kill").Icon(icons.Trash2).Enabled(len(a.term.tabs) > 0).Run(func() { a.closeTerminal(a.term.active) }),
		ui.Cmd("terminal.clear").Title("Terminal: Clear").Icon(icons.Eraser).Enabled(len(a.term.tabs) > 0).Run(func() {
			if t := a.activeTerminal(); t != nil {
				t.view.Clear()
			}
		}),
		ui.Cmd("panel.git").Title("Toggle Git").Shortcut(a.key("panel.git")).Icon(icons.GitBranch).Run(func() { a.togglePanel(panelGit) }),
		ui.Cmd("panel.problems").Title("Toggle Problems").Shortcut(a.key("panel.problems")).Icon(icons.TriangleAlert).Run(func() { a.togglePanel(panelProblems) }),
		ui.Cmd("panel.output").Title("Toggle Output").Icon(icons.ScrollText).Run(func() { a.togglePanel(panelOutput) }),
		ui.Cmd("panel.close").Title("Close Panel").Icon(icons.PanelBottomClose).Enabled(a.panelOpen).Run(func() { a.panelOpen = false }),
		ui.Cmd("agent.new").Title("Agent: New Session").Icon(icons.SquarePen).Run(func() { a.agentOpen = true; a.newAgentSession() }),
		ui.Cmd("agent.undo").Title("Agent: Undo Last Turn").Icon(icons.Undo2).Enabled(a.agent.lastCheckpoint != nil).Run(a.undoAgentTurn),
		ui.Cmd("agent.stop").Title("Agent: Stop").Icon(icons.Square).Enabled(a.agent.running).Run(a.stopAgent),
		ui.Cmd("agent.askSelection").Title("Ask Agent About Selection").Shortcut(a.key("agent.askSelection")).Icon(icons.Bot).
			Enabled(hasEd && ed.HasSelection()).Run(a.askAboutSelection),
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
	if x := a.idx; x != nil {
		if x.cmds == nil {
			x.cmds = make([]*ui.Command, 0, len(x.files))
			for _, p := range x.files {
				p := p
				rel := a.ws.Rel(p)
				x.cmds = append(x.cmds, ui.Item("open."+p).Title(filepath.Base(p)).Detail(filepath.Dir(rel)).
					Icon(icons.File).Scope(scopeFiles).Run(func() { a.openFile(p) }))
			}
		}
		cmds = append(cmds, x.cmds...)
	}
	cmds = append(cmds, a.locations...)
	c.Commands().Register(cmds...)
}

// searchProject opens the project search dialog.
func (a *App) searchProject() { a.openSearch() }

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
