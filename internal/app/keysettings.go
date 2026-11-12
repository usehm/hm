// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"sort"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/ui"
)

// keyRecorder is the shortcut being recorded for a command in Settings →
// Keyboard Shortcuts: up to two chords, pressed while its dialog is open.
type keyRecorder struct {
	id     string
	chords []string
}

// keyCommand is a command the shortcuts page lists.
type keyCommand struct{ id, title string }

// keysSection lists the commands with their shortcuts; Change records a
// new one into the user config.
func (a *App) keysSection(c *ui.Ctx) ui.View {
	th := c.Theme()
	q := strings.ToLower(a.kbQuery)
	rows := []ui.View{ui.TextField("keys-filter", a.kbQuery).Placeholder("Search commands or keys").
		IconStart(icons.Search).OnChange(func(v string) { a.kbQuery = v })}
	for _, kc := range a.keyCommands {
		key := a.key(kc.id)
		if q != "" && !strings.Contains(strings.ToLower(kc.title+" "+kc.id+" "+key), q) {
			continue
		}
		id := kc.id
		label := ui.View(ui.Muted("—"))
		if seq, err := ui.ParseKeySeq(key); err == nil && key != "" {
			label = ui.Kbd(seq.Label())
		}
		rows = append(rows, ui.Row(
			ui.Column(ui.Text(kc.title), ui.Caption(id)).Grow(1),
			label,
			ui.Button("keys-change-"+id, ui.Text("Change")).Subtle().OnClick(func() { a.recordKey(id) }),
		).Gap(th.Spacing.S).PaddingXY(th.Spacing.S, th.Spacing.XS).BorderBottom(ui.TokenBorder, th.Stroke.Thin))
	}
	rows = append(rows, ui.Caption("Shortcuts that apply only in some context go in keybindings: in the YAML."))
	return ui.Column(rows...).Gap(th.Spacing.XXS)
}

// rememberKeyCommands keeps the palette's commands for the shortcuts page.
func (a *App) rememberKeyCommands(cmds []*ui.Command) {
	if len(a.keyCommands) > 0 {
		return
	}
	for _, cmd := range cmds {
		if cmd.IsSection() || !strings.Contains(cmd.ID(), ".") {
			continue
		}
		a.keyCommands = append(a.keyCommands, keyCommand{cmd.ID(), cmd.DisplayTitle()})
	}
	sort.Slice(a.keyCommands, func(i, j int) bool { return a.keyCommands[i].title < a.keyCommands[j].title })
}

// recordKey asks for a new shortcut for command id.
func (a *App) recordKey(id string) {
	a.rec = &keyRecorder{id: id}
	title := id
	for _, kc := range a.keyCommands {
		if kc.id == id {
			title = kc.title
		}
	}
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title:      "Shortcut for " + title,
		Width:      420,
		FitContent: true,
		Body: func(c *ui.Ctx) ui.View {
			shown := ui.View(ui.Muted("Press the keys, or two chords in a row"))
			if r := a.rec; r != nil && len(r.chords) > 0 {
				shown = ui.Text(strings.Join(r.chords, " "))
			}
			return ui.Column(shown, ui.Caption("Mouse back and forward buttons work too.")).Gap(c.Theme().Spacing.S)
		},
		OnDismiss: func() { a.rec = nil },
		Actions: []ui.DialogAction{
			{Label: "Cancel", OnClick: func() { a.rec = nil }},
			{Label: "Remove", OnClick: func() { a.saveKey(id, "") }},
			{Label: "Save", Primary: true, OnClick: func() {
				if r := a.rec; r != nil && len(r.chords) > 0 {
					a.saveKey(id, strings.Join(r.chords, " "))
				}
			}},
		},
	})
}

func (a *App) saveKey(id, key string) {
	a.rec = nil
	a.setUserSetting([]string{"keys", id}, key)
}

// recordKeyEvent takes a key for the recorder; it reports whether it did.
func (a *App) recordKeyEvent(ev input.KeyEvent) bool {
	r := a.rec
	if r == nil || ev.Key == input.KeyEscape || ev.Key == input.KeyEnter && len(r.chords) > 0 {
		return false // the dialog cancels and saves
	}
	s := chordString(ev)
	if s == "" {
		return false
	}
	if len(r.chords) >= 2 {
		r.chords = r.chords[:0]
	}
	r.chords = append(r.chords, s)
	return true
}

// chordString writes ev the way config shortcuts are written, such as
// "cmd+shift+p".
func chordString(ev input.KeyEvent) string {
	name := keyNames[ev.Key]
	if name == "" {
		return ""
	}
	var parts []string
	if ev.Mods.Has(input.ModCtrl) {
		parts = append(parts, "ctrl")
	}
	if ev.Mods.Has(input.ModSuper) {
		parts = append(parts, "cmd")
	}
	if ev.Mods.Has(input.ModAlt) {
		parts = append(parts, "alt")
	}
	if ev.Mods.Has(input.ModShift) {
		parts = append(parts, "shift")
	}
	return strings.Join(append(parts, name), "+")
}

var keyNames = func() map[input.Key]string {
	m := map[input.Key]string{
		input.KeyLeft: "left", input.KeyRight: "right", input.KeyUp: "up", input.KeyDown: "down",
		input.KeyHome: "home", input.KeyEnd: "end", input.KeyPageUp: "pageup", input.KeyPageDown: "pagedown",
		input.KeyInsert: "insert", input.KeyBackspace: "backspace", input.KeyDelete: "delete",
		input.KeyEnter: "enter", input.KeyTab: "tab", input.KeyEscape: "escape", input.KeySpace: "space",
		input.KeyComma: ",", input.KeyPeriod: ".", input.KeySlash: "/", input.KeyMinus: "-", input.KeyEqual: "=",
		input.KeyLeftBracket: "[", input.KeyRightBracket: "]", input.KeyBacktick: "`", input.KeySemicolon: ";",
		input.KeyApostrophe: "'", input.KeyBackslash: `\`,
		input.KeyMouseMiddle: "mouse3", input.KeyMouseBack: "mouse4", input.KeyMouseForward: "mouse5",
	}
	fkeys := []input.Key{input.KeyF1, input.KeyF2, input.KeyF3, input.KeyF4, input.KeyF5, input.KeyF6,
		input.KeyF7, input.KeyF8, input.KeyF9, input.KeyF10, input.KeyF11, input.KeyF12}
	for i, k := range fkeys {
		m[k] = "f" + itoa(i+1)
	}
	for i := 0; i < 10; i++ {
		m[input.Key0+input.Key(i)] = itoa(i)
	}
	for r := 'a'; r <= 'z'; r++ {
		if k, err := ui.ParseChord(string(r)); err == nil {
			m[k.Key] = string(r)
		}
	}
	return m
}()
