package app

import (
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/workspace"
)

// key is the shortcut keys: gives command id, or "" when a keybinding
// removes it ("-id").
func (a *App) key(id string) string {
	cfg := a.cfg.Current()
	for _, kb := range cfg.Keybindings {
		if kb.Command == "-"+id {
			return ""
		}
	}
	return cfg.Keys[id]
}

// keybindingCmds are hidden commands for the keybindings list: each runs
// its command when its condition holds now.
func (a *App) keybindingCmds(c *ui.Ctx) []*ui.Command {
	kbs := a.cfg.Current().Keybindings
	if len(kbs) == 0 {
		return nil
	}
	ctx := a.whenContext(c)
	var out []*ui.Command
	for i, kb := range kbs {
		if kb.Command == "" || kb.Command[0] == '-' {
			continue
		}
		when, err := config.ParseWhen(kb.When)
		if err != nil {
			continue // validation already reported it
		}
		id := kb.Command
		out = append(out, ui.Cmd("keybinding."+itoa(i)).Title(id).Shortcut(kb.Key).Hidden(true).
			Enabled(when.Eval(ctx)).Run(func() { c.Commands().Run(id) }))
	}
	return out
}

// whenContext are the flags keybinding conditions test.
func (a *App) whenContext(c *ui.Ctx) map[string]bool {
	ed := a.ws.ActiveEditor()
	tab := a.ws.ActiveTab()
	ctx := map[string]bool{
		"editorFocus":   ed != nil && ed.Focused(),
		"hasSelection":  ed != nil && ed.HasSelection(),
		"multiCursor":   ed != nil && ed.Cursors() > 1,
		"agentView":     a.agentOpen,
		"panelOpen":     a.panelOpen,
		"searchOpen":    a.search.open,
		"gitTab":        tab != nil && tab.Kind == workspace.KindGit,
		"settingsTab":   tab != nil && tab.Kind == workspace.KindSettings,
		"terminalFocus": false,
		"agentFocus":    false,
	}
	if t := a.activeTerminal(); t != nil && a.panelOpen && a.panel == panelTerminal {
		ctx["terminalFocus"] = t.view.Focused()
	}
	if tf, ok := c.Widget("agent-input", func() any { return nil }).(*ui.TextInput); ok && tf != nil {
		ctx["agentFocus"] = tf.Focused()
	}
	return ctx
}
