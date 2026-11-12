package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/config"
)

func userConfig(t *testing.T, h *harness) string {
	t.Helper()
	p, _ := h.app.cfg.Paths()
	return p
}

func TestSettingsFormWritesYAMLKeepingComments(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	user := userConfig(t, h)
	if err := os.MkdirAll(config.UserDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "# mine\neditor:\n  font_size: 15 # big\n"
	if err := os.WriteFile(user, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	h.app.reloadConfig()
	h.app.openSettings()
	h.frame()

	h.app.setSetting([]string{"editor", "theme"}, "dracula")
	h.frame()
	if theme.Selected() != "dracula" {
		t.Fatalf("theme %q not applied", theme.Selected())
	}
	got, _ := os.ReadFile(user)
	for _, want := range []string{"# mine", "font_size: 15 # big", "theme: dracula"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
}

func TestEditAsYAMLAppliesOnSave(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.openConfigYAML(false)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	user := userConfig(t, h)
	if ed == nil || ed.Path != user {
		t.Fatal("Edit as YAML should open the user config")
	}
	ed.SetText(string(config.UserTemplate()) + "editor:\n  theme: nord\nkeys:\n  file.save: cmd+alt+w\n")
	h.frame()
	h.press(input.KeyS, input.ModSuper)
	if theme.Selected() != "nord" {
		t.Fatalf("theme %q, want nord", theme.Selected())
	}

	// The remapped chord saves; the old one no longer does.
	ed.SetText(string(config.UserTemplate()) + "editor:\n  theme: nord\nkeys:\n  file.save: cmd+alt+w\n# touched\n")
	h.frame()
	h.press(input.KeyS, input.ModSuper)
	if !ed.Modified() {
		t.Fatal("⌘S is no longer bound to save")
	}
	h.press(input.KeyW, input.ModSuper|input.ModAlt)
	if ed.Modified() {
		t.Fatal("⌘⌥W should save after the remap")
	}
}

func TestBrokenYAMLKeepsLastGood(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.setUserSetting([]string{"editor", "theme"}, "nord")
	h.app.openConfigYAML(false)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	ed.SetText("editor:\n  theme: gruvbox-dark\n  font_size: huge\n")
	h.frame()
	h.press(input.KeyS, input.ModSuper)
	if theme.Selected() != "nord" {
		t.Fatalf("broken config applied: theme %q", theme.Selected())
	}
	ps := h.app.cfg.Problems()
	if len(ps) != 1 || ps[0].Line != 3 || ps[0].Key != "editor.font_size" {
		t.Fatalf("problems %+v", ps)
	}
	if !hasNotification(h, "Settings not applied") {
		t.Fatal("expected an error notification")
	}
	ed.SetText("editor:\n  theme: gruvbox-dark\n")
	h.press(input.KeyS, input.ModSuper)
	if theme.Selected() != "gruvbox-dark" || len(h.app.cfg.Problems()) != 0 {
		t.Fatal("fixed config should apply")
	}
}

func TestExternalEditHotReloads(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	proj := config.ProjectPath(root)
	if err := os.MkdirAll(proj[:len(proj)-len("/config.yaml")], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proj, []byte("editor:\n  theme: solarized-dark\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for theme.Selected() != "solarized-dark" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		h.frame()
	}
	if theme.Selected() != "solarized-dark" {
		t.Fatal("project config change was not picked up")
	}
	if !hasNotification(h, "Settings applied") {
		t.Fatal("expected Settings applied")
	}
}

func hasNotification(h *harness, prefix string) bool {
	for _, n := range h.app.notifs.items {
		if strings.HasPrefix(n.text, prefix) {
			return true
		}
	}
	return false
}

func TestSettingsApplyLive(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.setSetting([]string{"editor", "scroll_past_end"}, false)
	if ui.EditorDefaults.ScrollPastEnd {
		t.Fatal("scroll_past_end not applied")
	}
	h.app.setSetting([]string{"editor", "scroll_past_end"}, true)
	if !ui.EditorDefaults.ScrollPastEnd {
		t.Fatal("scroll_past_end not applied")
	}

	// A new default mode reaches the agent while it uses the old default...
	h.app.newAgentSession()
	h.app.setSetting([]string{"agent", "default_mode"}, "lazy")
	if h.app.run().mode != "lazy" {
		t.Fatalf("agent mode %q, want the new default", h.app.run().mode)
	}
	// ...but not once the user picked one.
	h.app.run().mode = "auto"
	h.app.setSetting([]string{"agent", "default_mode"}, "rubber-duck")
	if h.app.run().mode != "auto" {
		t.Fatalf("agent mode %q, want the user's pick kept", h.app.run().mode)
	}
}
