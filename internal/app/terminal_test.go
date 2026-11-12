// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/layout"
	"github.com/mirzakhany/yoga/render"
)

// paint runs every element's painter, as the renderer would.
func (h *harness) paint(root *layout.Element) {
	dl := &render.DrawList{}
	var walk func(e *layout.Element)
	walk = func(e *layout.Element) {
		if e.Paint != nil {
			e.Paint(dl, h.c.Text())
		}
		for _, ch := range e.Children {
			walk(ch)
		}
	}
	walk(root)
	for _, o := range h.c.Overlays() {
		walk(o)
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.kb.TypeRune(r)
	}
	h.frame()
}

func TestTerminalPanel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell")
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PS1", "$ ")
	h := newHarness(t, project(t), false)

	h.press(input.KeyBacktick, input.ModCtrl)
	if !h.app.panelOpen || h.app.panel != panelTerminal || len(h.app.term.tabs) != 1 {
		t.Fatalf("ctrl+` should open a terminal: open=%v tabs=%d", h.app.panelOpen, len(h.app.term.tabs))
	}
	term := h.app.activeTerminal()
	h.until("terminal focus", 5*time.Second, term.view.Focused)

	h.typeText("echo hi-$((1+2))")
	h.press(input.KeyEnter, 0)
	h.until("command output", 10*time.Second, func() bool {
		return strings.Contains(term.view.Session().Text(true), "hi-3")
	})
	h.paint(h.frame())

	// Ctrl chords belong to the shell, Cmd chords to the app.
	h.press(input.KeyP, input.ModCtrl) // recalls the last command
	if h.c.Commands().Open {
		t.Fatal("ctrl+p must go to the shell, not the palette")
	}
	h.press(input.KeyC, input.ModCtrl) // and Ctrl+C drops it
	h.press(input.KeyP, input.ModSuper|input.ModShift)
	if !h.c.Commands().Open {
		t.Fatal("⌘⇧P should still open the palette")
	}
	h.press(input.KeyEscape, 0)

	// A second terminal, then closing it returns to the first.
	h.app.newTerminal()
	h.frame()
	if len(h.app.term.tabs) != 2 || h.app.term.active != 1 || h.app.term.tabs[1].name != "sh (2)" {
		t.Fatalf("second terminal: %d tabs, active %d", len(h.app.term.tabs), h.app.term.active)
	}
	h.app.closeTerminal(1)
	h.frame()
	h.frame()
	if len(h.app.term.tabs) != 1 || !term.view.Focused() {
		t.Fatal("closing a terminal focuses the one left")
	}

	// The toggle chord passes through the focused terminal and hides it.
	h.press(input.KeyBacktick, input.ModCtrl)
	if h.app.panelOpen {
		t.Fatal("ctrl+` should hide the terminal even while it has focus")
	}
	h.frame()
	if term.view.Focused() {
		t.Fatal("a hidden terminal must not keep focus")
	}

	// Exit ends the shell; the tab says so.
	h.press(input.KeyBacktick, input.ModCtrl)
	h.until("terminal focus again", 5*time.Second, term.view.Focused)
	h.typeText("exit 0")
	h.press(input.KeyEnter, 0)
	h.until("shell exit", 10*time.Second, func() bool { done, _ := term.view.Session().Exited(); return done })
	if !strings.Contains(term.title(), "exited 0") {
		t.Fatalf("title = %q", term.title())
	}
}
