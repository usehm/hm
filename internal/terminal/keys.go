// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"fmt"
	"runtime"

	"github.com/charmbracelet/x/vt"
	"github.com/mirzakhany/yoga/input"
)

// claims reports whether the terminal takes ev ahead of app shortcuts.
// It takes everything but Command chords on macOS; elsewhere Ctrl+Shift
// chords stay with the app (copy, paste, the palette), as in other
// terminals. Ctrl+Tab always moves focus.
func claims(ev input.KeyEvent) bool {
	m := ev.Mods
	if m.Has(input.ModSuper) || ev.Key == input.KeyTab && m.Has(input.ModCtrl) {
		return false
	}
	if runtime.GOOS != "darwin" && m.Has(input.ModCtrl|input.ModShift) {
		return false
	}
	return true
}

var namedKeys = map[input.Key]rune{
	input.KeyEnter: vt.KeyEnter, input.KeyTab: vt.KeyTab, input.KeyBackspace: vt.KeyBackspace,
	input.KeyEscape: vt.KeyEscape, input.KeyUp: vt.KeyUp, input.KeyDown: vt.KeyDown,
	input.KeyLeft: vt.KeyLeft, input.KeyRight: vt.KeyRight, input.KeyHome: vt.KeyHome,
	input.KeyEnd: vt.KeyEnd, input.KeyDelete: vt.KeyDelete, input.KeyInsert: vt.KeyInsert,
	input.KeyPageUp: vt.KeyPgUp, input.KeyPageDown: vt.KeyPgDown,
	input.KeyF1: vt.KeyF1, input.KeyF2: vt.KeyF2, input.KeyF3: vt.KeyF3, input.KeyF4: vt.KeyF4,
	input.KeyF5: vt.KeyF5, input.KeyF6: vt.KeyF6, input.KeyF7: vt.KeyF7, input.KeyF8: vt.KeyF8,
	input.KeyF9: vt.KeyF9, input.KeyF10: vt.KeyF10, input.KeyF11: vt.KeyF11, input.KeyF12: vt.KeyF12,
}

// ctrlKeys are the keys Ctrl turns into control characters.
var ctrlKeys = map[input.Key]rune{
	input.KeySpace: vt.KeySpace, input.KeyLeftBracket: '[', input.KeyRightBracket: ']',
	input.KeyBackslash: '\\', input.KeySlash: '_', input.KeyMinus: '_',
}

func init() {
	for i, k := range []input.Key{
		input.KeyA, input.KeyB, input.KeyC, input.KeyD, input.KeyE, input.KeyF, input.KeyG,
		input.KeyH, input.KeyI, input.KeyJ, input.KeyK, input.KeyL, input.KeyM, input.KeyN,
		input.KeyO, input.KeyP, input.KeyQ, input.KeyR, input.KeyS, input.KeyT, input.KeyU,
		input.KeyV, input.KeyW, input.KeyX, input.KeyY, input.KeyZ,
	} {
		ctrlKeys[k] = rune('a' + i)
	}
}

// xterm's modifier parameter: 1 + shift(1) + alt(2) + ctrl(4).
func modParam(m input.Mod) int {
	p := 1
	if m.Has(input.ModShift) {
		p++
	}
	if m.Has(input.ModAlt) {
		p += 2
	}
	if m.Has(input.ModCtrl) {
		p += 4
	}
	return p
}

// modifiedSeq encodes a modified cursor, paging or function key the way
// xterm does (Ctrl+Left is ESC[1;5D), which the emulator does not.
func modifiedSeq(k input.Key, m input.Mod) (string, bool) {
	if m&(input.ModShift|input.ModAlt|input.ModCtrl) == 0 {
		return "", false
	}
	mp := modParam(m)
	final := map[input.Key]byte{
		input.KeyUp: 'A', input.KeyDown: 'B', input.KeyRight: 'C', input.KeyLeft: 'D',
		input.KeyHome: 'H', input.KeyEnd: 'F',
		input.KeyF1: 'P', input.KeyF2: 'Q', input.KeyF3: 'R', input.KeyF4: 'S',
	}
	if f, ok := final[k]; ok {
		if runtime.GOOS == "darwin" && m == input.ModAlt {
			// Option+Left/Right move by word in shells, as in Terminal.app.
			switch k {
			case input.KeyLeft:
				return "\x1bb", true
			case input.KeyRight:
				return "\x1bf", true
			}
		}
		return fmt.Sprintf("\x1b[1;%d%c", mp, f), true
	}
	tilde := map[input.Key]int{
		input.KeyInsert: 2, input.KeyDelete: 3, input.KeyPageUp: 5, input.KeyPageDown: 6,
		input.KeyF5: 15, input.KeyF6: 17, input.KeyF7: 18, input.KeyF8: 19,
		input.KeyF9: 20, input.KeyF10: 21, input.KeyF11: 23, input.KeyF12: 24,
	}
	if n, ok := tilde[k]; ok {
		return fmt.Sprintf("\x1b[%d;%d~", n, mp), true
	}
	return "", false
}

// sendKey writes the bytes for one key press. It reports false for keys
// that arrive as text instead (plain and shifted printable keys).
func (s *Session) sendKeyEvent(ev input.KeyEvent) bool {
	m := ev.Mods
	if m.Has(input.ModCtrl) {
		if r, ok := ctrlKeys[ev.Key]; ok {
			k := vt.KeyPressEvent{Code: r, Mod: vt.ModCtrl}
			if m.Has(input.ModAlt) {
				k.Mod |= vt.ModAlt
			}
			s.SendKey(k)
			return true
		}
	}
	code, named := namedKeys[ev.Key]
	if !named {
		return false
	}
	if ev.Key != input.KeyTab && ev.Key != input.KeyEnter && ev.Key != input.KeyBackspace && ev.Key != input.KeyEscape {
		if seq, ok := modifiedSeq(ev.Key, m); ok {
			s.SendText(seq)
			return true
		}
	}
	k := vt.KeyPressEvent{Code: code}
	switch {
	case ev.Key == input.KeyTab && m.Has(input.ModShift):
		k.Mod = vt.ModShift
	case m.Has(input.ModAlt):
		k.Mod = vt.ModAlt
	}
	s.SendKey(k)
	return true
}
