package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/workspace"
)

func TestEditAndSaveWithShortcut(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	main := filepath.Join(root, "main.go")
	h.app.openFile(main)
	h.frame()

	ed := h.app.ws.ActiveEditor()
	if ed == nil || ed.Path != main {
		t.Fatal("main.go should be the active editor")
	}
	if !ed.Focused() {
		t.Fatal("active editor should take focus")
	}
	h.kb.TypeRune('/')
	h.kb.TypeRune('/')
	h.frame()
	if !h.app.ws.Modified(0) {
		t.Fatal("typing should mark the tab dirty")
	}
	h.press(input.KeyS, input.ModSuper)
	h.until("⌘S to save", 5*time.Second, func() bool { return !h.app.ws.Modified(0) })
	got, _ := os.ReadFile(main)
	if string(got) != "//package main\n\nfunc main() {}\n" {
		t.Fatalf("saved %q", got)
	}
}

func TestCloseDirtyTabAsks(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.openFile(filepath.Join(root, "main.go"))
	h.app.ws.ActiveEditor().SetText("changed")
	h.frame()
	h.press(input.KeyW, input.ModSuper)
	if len(h.app.ws.Tabs) != 1 {
		t.Fatal("dirty tab must not close without asking")
	}
	if !h.c.Dialogs().Open {
		t.Fatal("expected a save prompt")
	}
}

func TestSessionRestore(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, true)
	h.app.openFile(filepath.Join(root, "main.go"))
	h.app.openFile(filepath.Join(root, "util", "strings.go"))
	h.app.openSettings()
	h.app.ws.Active = 1
	h.app.ws.ActiveEditor().SetSelection(7, 7)
	h.frame()
	h.app.Close()
	if _, err := workspace.LoadSession(); err != nil {
		t.Fatal(err)
	}

	h2 := newHarness(t, root, true)
	ws := h2.app.ws
	if len(ws.Tabs) != 3 || ws.Active != 1 {
		t.Fatalf("restored tabs %+v active %d", ws.Tabs, ws.Active)
	}
	if ws.ActiveEditor().Caret() != 7 {
		t.Fatalf("caret %d", ws.ActiveEditor().Caret())
	}
}

func TestFrameRendersPanelsAndPalette(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.openGit()
	h.app.agentOpen = true
	h.app.openSettings()
	h.frame()
	h.press(input.KeyP, input.ModSuper|input.ModShift)
	if !h.c.Commands().Open {
		t.Fatal("⌘⇧P should open the palette")
	}
	h.press(input.KeyEscape, 0)
	h.app.idx.drain()
	waitIndex(t, h)
	h.app.showQuickOpen()
	h.frame()
	if !h.c.Commands().Open {
		t.Fatal("quick open should show the palette")
	}
}

func waitIndex(t *testing.T, h *harness) {
	t.Helper()
	for i := 0; i < 200 && len(h.app.idx.files) < 3; i++ {
		h.app.idx.drain()
		if len(h.app.idx.files) < 3 {
			sleepMs(5)
		}
	}
	if len(h.app.idx.files) != 3 {
		t.Fatalf("index has %d files", len(h.app.idx.files))
	}
}

func TestNewFileFromTreeMenu(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	items := h.app.treeMenu(root)
	if len(items) == 0 || items[0].Label != "New File…" {
		t.Fatalf("root menu %v", labels(items))
	}
	_ = ui.MenuItem{}
}

func labels(items []ui.MenuItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

func TestSessionRestoresViews(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, true)
	h.app.openFile(filepath.Join(root, "main.go"))
	h.app.openGit()
	h.app.toggleAgent()
	h.app.showPanel(panelProblems)
	h.frame()
	h.app.Close()

	h2 := newHarness(t, root, true)
	if !h2.app.agentOpen {
		t.Fatal("agent view not restored")
	}
	if !h2.app.panelOpen || h2.app.panel != panelProblems {
		t.Fatalf("panel open=%v kind=%v, want problems", h2.app.panelOpen, h2.app.panel)
	}
	if !h2.app.gitTabActive() {
		t.Fatal("git tab not restored as the active tab")
	}
	h2.frame()
}

func TestAgentToggleSwapsTheEditorFullScreen(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	if !h.app.agentFullscreen() {
		t.Fatal("full screen should be the default layout")
	}
	h.app.openFile(filepath.Join(root, "main.go"))
	h.frame()
	h.app.toggleAgent()
	h.frame()
	if !h.app.agentOpen || !h.app.agentFresh(h.app.run()) {
		t.Fatal("toggle should show a fresh agent")
	}
	if h.c.Widget(h.app.run().wid("input"), nil) == nil {
		t.Fatal("composer not built in the full-screen view")
	}
	h.app.toggleAgent()
	h.frame()
	if h.app.agentOpen {
		t.Fatal("toggle should go back to the editor")
	}
}
