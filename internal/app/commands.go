package app

import (
	"path/filepath"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"
)

// Scopes keep long lists out of the main palette.
const (
	scopeFiles     = "files"
	scopeThemes    = "themes"
	scopeTabs      = "tabs"
	scopeAgentTabs = "agentTabs"
	// scopeLocations lists references, definitions and symbols.
	scopeLocations = "locations"
)

// registerCommands lists every action. Menus, buttons and shortcuts all go
// through these ids.
func (a *App) registerCommands(c *ui.Ctx) {
	c.Commands().ToggleChord(a.key("palette"))
	ed := a.curEditor()
	hasEd := ed != nil
	agentTyping := a.run() != nil && agentInput(c, a.run()).Focused()
	// Language features run on the file's buffer, also for a split's mirror.
	lspEd := func() *ui.Editor { return a.lspTarget() }
	// Keys that mean something else in a text field, the editor's find bar included.
	edFocused := hasEd && ed.Focused() && !ed.FindFocused()
	cmds := []*ui.Command{
		ui.Section("File"),
		ui.Cmd("file.quickOpen").Title("Go to File…").Shortcut(a.key("file.quickOpen")).Icon(icons.Search).Run(a.showQuickOpen),
		ui.Cmd("file.openFolder").Title("Open Folder…").Shortcut(a.key("file.openFolder")).Icon(icons.FolderOpen).Run(a.askOpenFolder),
		ui.Cmd("file.newFile").Title("New File…").Shortcut(a.key("file.newFile")).Icon(icons.FilePlus).Run(func() { a.newEntry(a.newFileDir(), false) }),
		ui.Cmd("file.save").Title("Save").Shortcut(a.key("file.save")).Icon(icons.Save).Enabled(hasEd).Run(a.save),
		ui.Cmd("file.saveAll").Title("Save All").Shortcut(a.key("file.saveAll")).Icon(icons.Save).Run(func() { a.saveAll(false) }),
		ui.Cmd("file.close").Title("Close Tab").Shortcut(a.key("file.close")).Icon(icons.X).
			Enabled(a.ws.ActiveTab() != nil || a.agentShowsAlone()).Run(func() {
			// Full screen, the agent's tabs are the ones in view.
			if a.agentShowsAlone() {
				a.closeAgentTab(a.agent.active)
				return
			}
			a.closeTab(a.ws.Active)
		}),
		ui.Cmd("tabs.closeOthers").Title("Close Other Tabs").Shortcut(a.key("tabs.closeOthers")).Icon(icons.X).
			Enabled(len(a.ws.Tabs) > 1).Run(func() { a.closeOthers(a.ws.Active) }),
		ui.Cmd("tabs.closeRight").Title("Close Tabs to the Right").Shortcut(a.key("tabs.closeRight")).Icon(icons.X).
			Enabled(a.ws.Active >= 0 && a.ws.Active < len(a.ws.Tabs)-1).Run(func() { a.closeRight(a.ws.Active) }),
		ui.Cmd("tabs.closeSaved").Title("Close Saved Tabs").Shortcut(a.key("tabs.closeSaved")).Icon(icons.X).
			Enabled(len(a.ws.Tabs) > 0).Run(a.closeSaved),
		ui.Cmd("tabs.closeAll").Title("Close All Tabs").Shortcut(a.key("tabs.closeAll")).Icon(icons.X).
			Enabled(len(a.ws.Tabs) > 0).Run(a.closeAllTabs),
		ui.Cmd("view.splitRight").Title("Split Editor Right").Shortcut(a.key("view.splitRight")).Icon(icons.Columns2).
			Enabled(a.curPath() != "" && len(a.groups)+1 < maxGroups).Run(a.splitRight),
		ui.Cmd("view.moveToOtherGroup").Title("Move Editor to Other Group").Shortcut(a.key("view.moveToOtherGroup")).Icon(icons.ArrowRightLeft).
			Enabled(a.curPath() != "").Run(a.moveToOtherGroup),
		ui.Cmd("view.closeGroup").Title("Close Editor Group").Icon(icons.X).
			Enabled(a.focusedGroup() != nil).Run(func() { a.closeGroup(a.focusedGroup()) }),
		ui.Cmd("view.focusNextGroup").Title("Focus Next Editor Group").Shortcut(a.key("view.focusNextGroup")).Icon(icons.Columns2).
			Enabled(len(a.groups) > 0).Run(a.focusNextGroup),
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

		ui.Cmd("editor.addNextOccurrence").Title("Add Selection to Next Find Match").Shortcut(a.key("editor.addNextOccurrence")).
			Icon(icons.TextCursorInput).Enabled(edFocused).Run(func() { ed.AddNextOccurrence() }),
		ui.Cmd("editor.selectAllOccurrences").Title("Select All Occurrences").Shortcut(a.key("editor.selectAllOccurrences")).
			Icon(icons.TextCursorInput).Enabled(edFocused).Run(func() { ed.SelectAllOccurrences() }),
		ui.Cmd("editor.addCursorAbove").Title("Add Cursor Above").Shortcut(a.key("editor.addCursorAbove")).
			Icon(icons.TextCursorInput).Enabled(edFocused).Run(func() { ed.AddCursorLine(-1) }),
		ui.Cmd("editor.addCursorBelow").Title("Add Cursor Below").Shortcut(a.key("editor.addCursorBelow")).
			Icon(icons.TextCursorInput).Enabled(edFocused).Run(func() { ed.AddCursorLine(1) }),
		ui.Cmd("editor.toggleComment").Title("Toggle Line Comment").Shortcut(a.key("editor.toggleComment")).
			Icon(icons.MessageSquareCode).Enabled(edFocused).Run(func() { ed.Run(ui.ActionToggleComment) }),
		ui.Cmd("editor.duplicateLinesDown").Title("Duplicate Lines Down").Shortcut(a.key("editor.duplicateLinesDown")).
			Icon(icons.Copy).Enabled(edFocused).Run(func() { ed.Run(ui.ActionDuplicateLinesDown) }),
		ui.Cmd("editor.duplicateLinesUp").Title("Duplicate Lines Up").Shortcut(a.key("editor.duplicateLinesUp")).
			Icon(icons.Copy).Enabled(edFocused).Run(func() { ed.Run(ui.ActionDuplicateLinesUp) }),
		ui.Cmd("editor.moveLinesDown").Title("Move Lines Down").Shortcut(a.key("editor.moveLinesDown")).
			Icon(icons.ArrowDown).Enabled(edFocused).Run(func() { ed.Run(ui.ActionMoveLinesDown) }),
		ui.Cmd("editor.moveLinesUp").Title("Move Lines Up").Shortcut(a.key("editor.moveLinesUp")).
			Icon(icons.ArrowUp).Enabled(edFocused).Run(func() { ed.Run(ui.ActionMoveLinesUp) }),
		ui.Cmd("editor.deleteLines").Title("Delete Lines").Shortcut(a.key("editor.deleteLines")).
			Icon(icons.Trash2).Enabled(edFocused).Run(func() { ed.Run(ui.ActionDeleteLines) }),
		ui.Cmd("editor.fold").Title("Fold").Shortcut(a.key("editor.fold")).
			Icon(icons.ChevronRight).Enabled(edFocused).Run(func() { ed.Run(ui.ActionFold) }),
		ui.Cmd("editor.unfold").Title("Unfold").Shortcut(a.key("editor.unfold")).
			Icon(icons.ChevronDown).Enabled(edFocused).Run(func() { ed.Run(ui.ActionUnfold) }),
		ui.Cmd("editor.foldAll").Title("Fold All").Shortcut(a.key("editor.foldAll")).
			Icon(icons.ChevronsDownUp).Enabled(hasEd).Run(func() { ed.FoldAll() }),
		ui.Cmd("editor.unfoldAll").Title("Unfold All").Shortcut(a.key("editor.unfoldAll")).
			Icon(icons.ChevronsUpDown).Enabled(hasEd).Run(func() { ed.UnfoldAll() }),

		ui.Cmd("nav.back").Title("Go Back").Shortcut(a.key("nav.back")).Icon(icons.ArrowLeft).
			Enabled(len(a.nav.back) > 0).Run(a.navBack),
		ui.Cmd("nav.forward").Title("Go Forward").Shortcut(a.key("nav.forward")).Icon(icons.ArrowRight).
			Enabled(len(a.nav.fwd) > 0).Run(a.navForward),
		// The mouse's back and forward buttons, as a second shortcut.
		ui.Cmd("nav.backMouse").Shortcut(a.key("nav.backMouse")).Hidden(true).Enabled(len(a.nav.back) > 0).Run(a.navBack),
		ui.Cmd("nav.forwardMouse").Shortcut(a.key("nav.forwardMouse")).Hidden(true).Enabled(len(a.nav.fwd) > 0).Run(a.navForward),

		ui.Section("Code"),
		ui.Cmd("editor.definition").Title("Go to Definition").Shortcut(a.key("editor.definition")).Icon(icons.SquareArrowOutUpRight).
			Enabled(hasEd).Run(func() { e := lspEd(); a.definition(e, e.Caret()) }),
		ui.Cmd("editor.implementation").Title("Go to Implementation").Shortcut(a.key("editor.implementation")).Icon(icons.SquareArrowOutUpRight).
			Enabled(hasEd).Run(func() { e := lspEd(); a.implementation(e, e.Caret()) }),
		ui.Cmd("editor.references").Title("Find References").Shortcut(a.key("editor.references")).Icon(icons.ListTree).
			Enabled(hasEd).Run(func() { e := lspEd(); a.references(e, e.Caret()) }),
		ui.Cmd("editor.rename").Title("Rename Symbol").Shortcut(a.key("editor.rename")).Icon(icons.PencilLine).
			Enabled(hasEd).Run(func() { a.rename(lspEd()) }),
		ui.Cmd("editor.symbols").Title("Go to Symbol in File…").Shortcut(a.key("editor.symbols")).Icon(icons.Hash).
			Enabled(hasEd).Run(func() { a.documentSymbols(lspEd()) }),
		ui.Cmd("editor.format").Title("Format Document").Shortcut(a.key("editor.format")).Icon(icons.WandSparkles).
			Enabled(hasEd).Run(func() { a.format(lspEd(), "manual", nil) }),
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
		ui.Cmd("explorer.collapseAll").Title("Explorer: Collapse All Folders").Icon(icons.ChevronsDownUp).Run(func() { a.tree.CollapseAll() }),
		ui.Cmd("view.theme").Title("Color Theme…").Shortcut(a.key("view.theme")).Icon(icons.Palette).Run(func() {
			c.Commands().ShowScope(scopeThemes, "Select a color theme")
		}),
		ui.Cmd("view.zoomIn").Title("Zoom In").Shortcut(a.key("view.zoomIn")).Icon(icons.ZoomIn).Run(func() { a.zoomStep(1) }),
		ui.Cmd("view.zoomOut").Title("Zoom Out").Shortcut(a.key("view.zoomOut")).Icon(icons.ZoomOut).Run(func() { a.zoomStep(-1) }),
		ui.Cmd("view.zoomReset").Title("Reset Zoom").Shortcut(a.key("view.zoomReset")).Icon(icons.RotateCcw).Run(func() { a.setZoom(1) }),
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
		ui.Cmd("panel.git").Title("Toggle Git").Shortcut(a.key("panel.git")).Icon(icons.GitBranch).Run(a.toggleGit),
		ui.Cmd("panel.problems").Title("Toggle Problems").Shortcut(a.key("panel.problems")).Icon(icons.TriangleAlert).Run(func() { a.togglePanel(panelProblems) }),
		ui.Cmd("panel.output").Title("Toggle Output").Icon(icons.ScrollText).Run(func() { a.togglePanel(panelOutput) }),
		ui.Cmd("panel.close").Title("Close Panel").Icon(icons.PanelBottomClose).Enabled(a.panelOpen).Run(func() { a.panelOpen = false }),
		ui.Cmd("agent.new").Title("Agent: New Session").Icon(icons.SquarePen).Run(func() { a.agentOpen = true; a.newAgentSession() }),
		ui.Cmd("agent.newTab").Title("Agent: New Session Tab").Shortcut(a.key("agent.newTab")).Icon(icons.Plus).
			Enabled(a.agentOpen).Run(a.newAgentSession),
		ui.Cmd("agent.closeTab").Title("Agent: Close Tab").Shortcut(a.key("agent.closeTab")).Icon(icons.X).
			Enabled(a.agentOpen && len(a.agent.tabs) > 0).Run(func() { a.closeAgentTab(a.agent.active) }),
		ui.Cmd("agent.nextTab").Title("Agent: Next Tab").Shortcut(a.key("agent.nextTab")).Icon(icons.ChevronRight).
			Enabled(a.agentOpen && len(a.agent.tabs) > 1).Run(func() { a.cycleAgentTab(1) }),
		ui.Cmd("agent.prevTab").Title("Agent: Previous Tab").Shortcut(a.key("agent.prevTab")).Icon(icons.ChevronLeft).
			Enabled(a.agentOpen && len(a.agent.tabs) > 1).Run(func() { a.cycleAgentTab(-1) }),
		ui.Cmd("agent.tabs").Title("Agent: Go to Tab…").Shortcut(a.key("agent.tabs")).Icon(icons.Layers).
			Enabled(len(a.agent.tabs) > 0).Run(func() { c.Commands().ShowScope(scopeAgentTabs, "Go to agent tab") }),
		ui.Cmd("agent.projects").Title("Agent: Projects").Icon(icons.FolderKanban).Run(func() { a.openAgentPage(agentTabProjects, "") }),
		ui.Cmd("agent.settings").Title("Agent: Settings").Icon(icons.Settings).Run(func() { a.openAgentPage(agentTabSettings, "") }),
		ui.Cmd("agent.undo").Title("Agent: Undo Last Turn").Icon(icons.Undo2).Enabled(a.run() != nil && !a.run().running && a.run().hasPrompt()).Run(func() {
			if r := a.run(); r != nil {
				a.undoAgentTurn(r)
			}
		}),
		ui.Cmd("agent.tree").Title("Agent: Session Tree").Icon(icons.ListTree).Enabled(a.run() != nil).Run(func() {
			if r := a.run(); r != nil {
				a.openTree(r)
			}
		}),
		// Enter in the message box steers; these send it either way, so
		// keybindings can swap them.
		ui.Cmd("agent.steer").Title("Agent: Send, Steering the Running Turn").Shortcut(a.key("agent.steer")).Icon(icons.CornerDownLeft).
			Enabled(agentTyping).Run(func() { a.sendRunAs(a.run(), false) }),
		ui.Cmd("agent.followUp").Title("Agent: Send After the Running Turn").Shortcut(a.key("agent.followUp")).Icon(icons.Clock).
			Enabled(agentTyping).Run(func() { a.sendRunAs(a.run(), true) }),
		ui.Cmd("agent.stop").Title("Agent: Stop").Icon(icons.Square).Enabled(a.run() != nil && a.run().running).Run(a.stopAgent),
		ui.Cmd("agent.askSelection").Title("Ask Agent About Selection").Shortcut(a.key("agent.askSelection")).Icon(icons.Bot).
			Enabled(hasEd && ed.HasSelection()).Run(a.askAboutSelection),
		ui.Cmd("agent.toggle").Title("Toggle Agent").Shortcut(a.key("agent.toggle")).Icon(icons.Bot).Run(a.toggleAgent),
		ui.Section("Settings"),
		ui.Cmd("settings.yaml").Title("Settings: Open User Config (YAML)").Icon(icons.FileCode).Run(func() { a.openConfigYAML(false) }),
		ui.Cmd("settings.projectYaml").Title("Settings: Open Project Config (YAML)").Icon(icons.FileCode).Run(func() { a.openConfigYAML(true) }),
		ui.Cmd("settings.defaults").Title("Settings: Show Default Config").Icon(icons.FileText).Run(a.openDefaults),
		ui.Cmd("settings.reload").Title("Settings: Reload Config").Icon(icons.RefreshCw).Run(a.reloadConfig),
		ui.Cmd("agent.guard").Title("Agent: Edit Guard Rules (guard.yaml)").Icon(icons.ShieldCheck).Run(func() { a.openGuard(false) }),
		ui.Cmd("app.settings").Title("Open Settings").Shortcut(a.key("app.settings")).Icon(icons.Settings).Run(a.openSettings),
	}

	a.rememberKeyCommands(cmds)
	for _, name := range theme.Names() {
		n := name
		cmds = append(cmds, ui.Item("theme."+n).Title(n).Icon(icons.Palette).Scope(scopeThemes).Run(func() { a.setUserSetting([]string{"editor", "theme"}, n) }))
	}
	for i, t := range a.agent.tabs {
		i := i
		m := a.agentTabModel(c, t)
		cmds = append(cmds, ui.Item("agenttab."+itoa(i)+"."+t.id()).Title(m.Title).Detail(t.kind).
			Icon(m.Icon).Scope(scopeAgentTabs).Run(func() { a.agentOpen = true; a.focusAgentTab(i) }))
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
	// Keybindings go first: the first enabled command a key matches runs.
	c.Commands().Register(append(a.keybindingCmds(c), cmds...)...)
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
