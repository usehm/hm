package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mirzakhany/yoga/input"

	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/workspace"
)

func TestKeybindingsWithConditionsAndSequences(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	if err := os.MkdirAll(config.UserDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	user := userConfig(t, h)
	if err := os.WriteFile(user, []byte(`keybindings:
  - { key: "cmd+k cmd+o", command: app.settings }
  - { key: "f6", command: panel.problems, when: "editorFocus" }
  - { command: "-file.quickOpen" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	h.app.reloadConfig()
	if h.app.key("file.quickOpen") != "" {
		t.Fatal("-file.quickOpen should remove its shortcut")
	}
	h.frame()

	h.press(input.KeyF6, 0)
	if h.app.panelOpen {
		t.Fatal("f6 runs only while the editor has focus")
	}
	h.app.openFile(filepath.Join(root, "main.go"))
	h.frame()
	h.frame()
	h.press(input.KeyF6, 0)
	if !h.app.panelOpen || h.app.panel != panelProblems {
		t.Fatal("f6 should open the problems panel from the editor")
	}

	h.press(input.KeyK, input.ModSuper)
	h.press(input.KeyO, input.ModSuper)
	if h.app.ws.ActiveTab().Kind != workspace.KindSettings {
		t.Fatal("cmd+k cmd+o should open settings")
	}
}

func TestRecordShortcutInSettings(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.openSettings()
	h.app.settingsCat = "keys"
	h.frame()
	if len(h.app.keyCommands) == 0 {
		t.Fatal("no commands listed")
	}
	h.app.recordKey("app.settings")
	h.frame()
	h.press(input.KeyK, input.ModSuper)
	h.press(input.KeyJ, input.ModSuper|input.ModShift)
	if got := h.app.rec.chords; len(got) != 2 || got[0] != "cmd+k" || got[1] != "cmd+shift+j" {
		t.Fatalf("recorded %v", got)
	}
	h.app.saveKey("app.settings", "cmd+k cmd+shift+j")
	if k := h.app.key("app.settings"); k != "cmd+k cmd+shift+j" {
		t.Fatalf("saved key %q", k)
	}
	h.app.saveKey("app.settings", "")
	if k := h.app.key("app.settings"); k != "" || len(h.app.cfg.Problems()) > 0 {
		t.Fatalf("removed key %q, problems %v", k, h.app.cfg.Problems())
	}
}
