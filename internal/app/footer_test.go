// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yogaui/yoga/ui"
)

func menuItem(t *testing.T, items []ui.MenuItem, label string) ui.MenuItem {
	t.Helper()
	for _, it := range items {
		if it.Label == label {
			return it
		}
	}
	t.Fatalf("no %q in %+v", label, items)
	return ui.MenuItem{}
}

func TestFooterKeymapMenu(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.openFile(filepath.Join(root, "main.go"))
	h.frame()

	if !menuItem(t, h.app.keymapMenu(), "Default").Checked {
		t.Fatal("default keymap should be checked")
	}
	menuItem(t, h.app.keymapMenu(), "Vim").OnSelect()
	h.frame()
	if !h.app.vimOn() || h.app.vimState() == nil {
		t.Fatal("Vim should be on")
	}
	user, _ := h.app.cfg.Paths()
	if data, _ := os.ReadFile(user); !strings.Contains(string(data), "keymap: vim") {
		t.Fatalf("user config: %s", data)
	}
	menuItem(t, h.app.keymapMenu(), "Default").OnSelect()
	h.frame()
	if h.app.vimOn() {
		t.Fatal("Vim should be off")
	}
}

func TestFooterKeymapWinsOverProject(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.writeSetting(true, []string{"editor", "keymap"}, "vim")
	h.app.setKeymap("default")
	if h.app.vimOn() {
		t.Fatal("the project file's vim should be switched off too")
	}
}

func TestFooterLanguageMenu(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	txt := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(txt, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if it := h.app.languageMenu(txt); len(it) != 1 || !it[0].Disabled {
		t.Fatalf("plain text menu: %+v", it)
	}
	main := filepath.Join(root, "main.go")
	items := h.app.languageMenu(main)
	if h.app.lang.MissingServer(main) == nil {
		menuItem(t, items, "Restart gopls")
		menuItem(t, items, "Show Output").OnSelect()
		if !h.app.panelOpen || h.app.panel != panelOutput {
			t.Fatal("Show Output should open the output panel")
		}
	}
}

func TestWindowTitleNamesTheFolder(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	if got := h.app.windowTitle(); got != "hm — "+filepath.Base(root) {
		t.Fatalf("title %q", got)
	}
}

func TestNextInstances(t *testing.T) {
	pids := []int{10, 20, 30, 40}
	for _, tc := range []struct {
		self, dir int
		want      []int
	}{
		{20, 1, []int{30, 40, 10}},
		{20, -1, []int{10, 40, 30}},
		{40, 1, []int{10, 20, 30}},
		{25, 1, []int{30, 40, 10, 20}}, // not recorded itself
	} {
		if got := nextInstances(pids, tc.self, tc.dir); !slices.Equal(got, tc.want) {
			t.Errorf("self %d dir %d: %v, want %v", tc.self, tc.dir, got, tc.want)
		}
	}
}

func TestDetectTextFormat(t *testing.T) {
	for _, c := range []struct {
		text     string
		eol, enc string
	}{
		{"a\nb\n", eolLF, encUTF8},
		{"a\r\nb\r\n", eolCRLF, encUTF8},
		{"one line", eolLF, encUTF8},
		{"\xEF\xBB\xBFa\r\n", eolCRLF, encUTF8BOM},
		{"\xFF\xFEa\x00", eolLF, encUTF16LE},
		{"\xFE\xFF\x00a", eolLF, encUTF16BE},
		{"caf\xE9\n", eolLF, encBinary},
	} {
		if eol, enc := detectEOL([]byte(c.text)), detectEncoding([]byte(c.text)); eol != c.eol || enc != c.enc {
			t.Errorf("%q: %s %s, want %s %s", c.text, eol, enc, c.eol, c.enc)
		}
	}
}

// The footer's line endings and BOM items rewrite the buffer, undoably.
func TestFooterLineEndingsAndBOM(t *testing.T) {
	root := project(t)
	path := filepath.Join(root, "dos.txt")
	if err := os.WriteFile(path, []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, root, false)
	h.app.openFile(path)
	h.frame()
	ed := h.app.curEditor()
	if !menuItem(t, h.app.eolMenu(ed), "CRLF (\\r\\n)").Checked {
		t.Fatal("CRLF should be checked")
	}
	menuItem(t, h.app.eolMenu(ed), "LF (\\n)").OnSelect()
	if got := string(ed.Bytes()); got != "a\nb\n" {
		t.Fatalf("to LF: %q", got)
	}
	if !menuItem(t, h.app.eolMenu(ed), "LF (\\n)").Checked {
		t.Fatal("LF should be checked after converting")
	}
	menuItem(t, h.app.encodingMenu(ed), encUTF8BOM).OnSelect()
	if got := string(ed.Bytes()); got != "\xEF\xBB\xBFa\nb\n" {
		t.Fatalf("with BOM: %q", got)
	}
	menuItem(t, h.app.encodingMenu(ed), encUTF8).OnSelect()
	if got := string(ed.Bytes()); got != "a\nb\n" {
		t.Fatalf("without BOM: %q", got)
	}
}

// Indentation picked in the footer holds for that file over a config reload.
func TestFooterIndentation(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	path := filepath.Join(root, "main.go")
	h.app.openFile(path)
	h.frame()
	ed := h.app.curEditor()
	menuItem(t, h.app.indentMenu(ed, path), "Indent Using Spaces").OnSelect()
	menuItem(t, h.app.indentMenu(ed, path), "Width 2").OnSelect()
	if !ed.IndentSpaces() || ed.TabWidth() != 2 {
		t.Fatalf("spaces %v width %d", ed.IndentSpaces(), ed.TabWidth())
	}
	h.app.applyAllEditorSettings()
	if !ed.IndentSpaces() || ed.TabWidth() != 2 {
		t.Fatal("a reload should keep the footer's pick")
	}
	menuItem(t, h.app.indentMenu(ed, path), "Use Settings").OnSelect()
	if es := h.app.editorSettings(path); ed.IndentSpaces() != es.InsertSpaces || ed.TabWidth() != es.TabWidth {
		t.Fatal("Use Settings should bring the config back")
	}
}
