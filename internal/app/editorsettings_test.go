// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
)

func TestLanguageEditorSettings(t *testing.T) {
	root := project(t)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), `
languages:
  markdown:
    editor: { tab_width: 3, trim_trailing_whitespace: true, auto_indent: none }
`)
	y := filepath.Join(root, "a.yaml")
	md := filepath.Join(root, "notes.md")
	writeFile(t, y, "a:\n")
	writeFile(t, md, "x  \ny\t\n")
	h := newHarness(t, root, false)

	h.app.openFile(y)
	h.frame()
	ed := h.app.ws.ActiveEditor()
	if ed.TabWidth() != 2 || !ed.IndentSpaces() {
		t.Fatalf("yaml: tab %d spaces %v, want 2 and spaces", ed.TabWidth(), ed.IndentSpaces())
	}
	// advanced auto indent from the defaults: a level after the colon.
	ed.Focus()
	ed.SetSelection(2, 2)
	h.press(input.KeyEnter, 0)
	if got := string(ed.Bytes()); got != "a:\n  \n" {
		t.Fatalf("yaml enter gave %q", got)
	}

	h.app.openFile(md)
	h.frame()
	ed = h.app.ws.ActiveEditor()
	if ed.TabWidth() != 3 {
		t.Fatalf("markdown tab %d, want the project's 3", ed.TabWidth())
	}
	ed.SetText("x  \ny\t\n")
	h.frame()
	h.press(input.KeyS, input.ModSuper)
	h.until("trimmed save", 5*time.Second, func() bool { return !ed.Modified() })
	if got, _ := os.ReadFile(md); string(got) != "x\ny\n" {
		t.Fatalf("saved %q, want trailing blanks trimmed", got)
	}

	// Editing the project config reapplies to open editors.
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), "languages:\n  markdown:\n    editor: { tab_width: 6 }\n")
	h.until("reload", 5*time.Second, func() bool { return ed.TabWidth() == 6 })
}

func TestSettingsLanguagePicker(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.openSettings()
	h.app.settingsCat = "editor"
	h.app.settingsProject = true
	h.app.settingsLang = "yaml"
	h.frame()

	h.app.setSetting([]string{"languages", "yaml", "editor", "tab_width"}, 6)
	h.frame()
	if es := h.app.editorSettings(filepath.Join(root, "a.yaml")); es.TabWidth != 6 || !es.InsertSpaces {
		t.Fatalf("yaml settings %+v, want tab 6 with the built-in spaces", es)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".hm", "config.yaml"))
	if !strings.Contains(string(data), "tab_width: 6") {
		t.Fatalf("project file:\n%s", data)
	}

	h.app.unsetSetting([]string{"languages", "yaml", "editor"})
	h.frame()
	if es := h.app.editorSettings(filepath.Join(root, "a.yaml")); es.TabWidth != 2 {
		t.Fatalf("reset should go back to the built-in 2, got %d", es.TabWidth)
	}
	if ps := h.app.cfg.Problems(); len(ps) > 0 {
		t.Fatal(ps)
	}

	h.app.settingsLang = "no-such-language"
	h.frame() // falls back to all languages
}
