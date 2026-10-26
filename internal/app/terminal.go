package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/terminal"
)

// termTab is one terminal in the bottom panel.
type termTab struct {
	view *terminal.View
	name string // the shell's name, numbered when there are several
}

type terminals struct {
	tabs   []*termTab
	active int
	seq    int
	// focus asks the next frame to focus the active terminal.
	focus bool
}

// newTerminal starts the configured shell in the workspace root and shows it.
func (a *App) newTerminal() {
	a.showPanel(panelTerminal)
	cfg := a.cfg.Current().Terminal
	cmd := terminal.ShellCommand(cfg.Shell, a.ws.Root)
	s, err := terminal.Start(cmd, 80, 24, a.invalidate)
	if err != nil {
		a.notifs.Add("Could not start the terminal: "+err.Error(), ui.ToastError)
		a.toast("Could not start the terminal", ui.ToastError)
		return
	}
	v := terminal.NewView(s)
	v.Pass = a.terminalPasses
	a.term.seq++
	name := strings.TrimSuffix(filepath.Base(cmd.Path), ".exe")
	if a.term.seq > 1 {
		name = fmt.Sprintf("%s (%d)", name, a.term.seq)
	}
	a.term.tabs = append(a.term.tabs, &termTab{view: v, name: name})
	a.term.active = len(a.term.tabs) - 1
	a.term.focus = true
}

// terminalPasses keeps app chords written with Ctrl (Toggle Terminal is
// Ctrl+` by default) working while a terminal has focus. Chords written
// with Cmd never reach the terminal on macOS anyway.
func (a *App) terminalPasses(ev input.KeyEvent) bool {
	for _, chord := range a.cfg.Current().Keys {
		if !strings.Contains(strings.ToLower(chord), "ctrl") {
			continue
		}
		if ch, err := ui.ParseChord(chord); err == nil && ch.Matches(ev) {
			return true
		}
	}
	return false
}

func (a *App) activeTerminal() *termTab {
	if a.term.active < 0 || a.term.active >= len(a.term.tabs) {
		return nil
	}
	return a.term.tabs[a.term.active]
}

// closeTerminal ends the shell in tab i and removes the tab.
func (a *App) closeTerminal(i int) {
	if i < 0 || i >= len(a.term.tabs) {
		return
	}
	_ = a.term.tabs[i].view.Session().Close()
	a.term.tabs = append(a.term.tabs[:i], a.term.tabs[i+1:]...)
	if a.term.active >= len(a.term.tabs) {
		a.term.active = len(a.term.tabs) - 1
	}
	if len(a.term.tabs) == 0 {
		a.term.seq = 0
		a.panelOpen = false
		return
	}
	a.term.focus = true
}

func (a *App) closeAllTerminals() {
	for _, t := range a.term.tabs {
		_ = t.view.Session().Close()
	}
	a.term.tabs = nil
}

func (t *termTab) title() string {
	s := t.view.Session()
	title := t.name
	if pt := s.Title(); pt != "" {
		title = pt
	}
	if done, code := s.Exited(); done {
		return fmt.Sprintf("%s — exited %d", title, code)
	}
	return title
}

func (a *App) terminalView(c *ui.Ctx) ui.View {
	if len(a.term.tabs) == 0 {
		a.newTerminal()
		if len(a.term.tabs) == 0 {
			return ui.EmptyState("Terminal", "The shell did not start; see Notifications.").EmptyIcon(icons.SquareTerminal)
		}
	}
	models := make([]ui.TabModel, len(a.term.tabs))
	for i, t := range a.term.tabs {
		models[i] = ui.TabModel{Title: t.title(), Icon: icons.SquareTerminal}
	}
	cur := a.activeTerminal()
	if a.term.focus {
		// The drawer skips its panel while it starts to open, which drops
		// focus again; ask until focus outlasts a frame.
		if cur.view.Focused() {
			a.term.focus = false
		} else {
			c.Focus().Focus(cur.view)
		}
	}
	if cfg := a.cfg.Current().Terminal; cfg.FontSize > 0 {
		cur.view.FontSize = float32(cfg.FontSize)
	}
	v := cur.view
	menu := []ui.MenuItem{
		{Label: "Copy", Disabled: !v.HasSelection(), OnSelect: v.Copy},
		{Label: "Paste", OnSelect: v.Paste},
		{Label: "Select All", OnSelect: v.SelectAll},
		{Label: "Clear", OnSelect: v.Clear},
		{Label: "New Terminal", OnSelect: a.newTerminal},
		{Label: "Kill Terminal", OnSelect: func() { a.closeTerminal(a.term.active) }},
	}
	bar := ui.Row(
		ui.Tabs("term-tabs", models).
			Selected(a.term.active).
			OnSelectItem(func(i int, _ string) { a.term.active = i; a.term.focus = true }).
			OnTabClose(a.closeTerminal).
			Grow(1),
		ui.IconButton("term-new", icons.Plus).Height(22).Tooltip("New Terminal").OnClick(a.newTerminal),
	).Align(ui.AlignCenter)
	return ui.Column(
		bar,
		ui.ContextMenu("term-menu", ui.ViewOf(v).Grow(1), menu).Grow(1),
	).Grow(1)
}
