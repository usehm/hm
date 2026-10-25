package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/layout"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/shape"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/workspace"
)

// harness drives App frames the way yoga's GPU loop does.
type harness struct {
	t     *testing.T
	app   *App
	c     *ui.Ctx
	mouse *input.Mouse
	kb    *input.Keyboard
	root  string
}

func newHarness(t *testing.T, root string, session bool) *harness {
	t.Helper()
	text, err := shape.NewEngine(1, false)
	if err != nil {
		t.Skip(err)
	}
	clip := &input.MemClipboard{}
	sheet := render.NewSpriteSheet(text.Atlas)
	yoga.SetResources(text, sheet, clip)
	c := ui.New(text, ui.NewFocusScope(), nil)
	c.SetIcons(sheet)
	c.SetClipboard(clip)
	h := &harness{t: t, c: c, mouse: &input.Mouse{}, kb: &input.Keyboard{}, root: root}
	h.app = Build(Options{Root: root, NoSession: !session, KeepPath: true, NoUpdates: true})
	t.Cleanup(h.app.cfg.Close)
	h.frame()
	return h
}

// frame runs one input pass and one paint pass.
func (h *harness) frame() *layout.Element {
	a := h.app
	root := ui.BuildFrame(h.c, a.Body, 1280, 820, h.mouse, h.kb)
	layout.Dispatch(root, h.mouse)
	h.c.Focus().HandleMouse(h.mouse)
	h.c.Commands().Dispatch(h.kb)
	kept := h.kb.Keys[:0]
	for _, k := range h.kb.Keys {
		if !a.OnKey(h.c, k) {
			kept = append(kept, k)
		}
	}
	h.kb.Keys = kept
	h.c.Focus().Route(h.kb)
	h.mouse.EndFrame()
	h.kb.EndFrame()
	return ui.BuildFrame(h.c, a.Body, 1280, 820, h.mouse, h.kb)
}

// until runs frames until cond holds or the deadline passes.
func (h *harness) until(what string, d time.Duration, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
		h.frame()
	}
}

func (h *harness) press(key input.Key, mods input.Mod) {
	h.kb.PressKey(key, mods)
	h.frame()
}

// project writes a small Go module and points app state at a temp dir.
func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	state := t.TempDir()
	workspace.StateDir = func() string { return filepath.Join(state, "state") }
	config.UserDir = func() string { return filepath.Join(state, "config") }
	files := map[string]string{
		"go.mod":          "module example.com/demo\n\ngo 1.22\n",
		"main.go":         "package main\n\nfunc main() {}\n",
		"util/strings.go": "package util\n",
	}
	for p, s := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
