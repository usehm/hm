// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	codeeditor "github.com/yogaui/editor"
	"github.com/yogaui/yoga/icons"
	"github.com/yogaui/yoga/ui"
)

// Buffers keep a file's bytes as they are on disk, so the footer reads the
// line endings and encoding off the text, and changing them is an edit.

// Line endings.
const (
	eolLF   = "LF"
	eolCRLF = "CRLF"
)

// Encodings the footer names.
const (
	encUTF8    = "UTF-8"
	encUTF8BOM = "UTF-8 with BOM"
	encUTF16LE = "UTF-16 LE"
	encUTF16BE = "UTF-16 BE"
	encBinary  = "Unknown"
)

var bomUTF8 = []byte{0xEF, 0xBB, 0xBF}

// textFormat is what the footer shows about an editor's bytes.
type textFormat struct {
	ed       *codeeditor.Editor
	rev      uint64
	eol, enc string
}

// formatOf reads ed's line endings and encoding. The footer asks each
// frame for the one editor it shows, so only that one is remembered.
func (a *App) formatOf(ed *codeeditor.Editor) textFormat {
	if f := a.footerFmt; f.ed == ed && f.rev == ed.Revision() {
		return f
	}
	text := ed.Bytes()
	a.footerFmt = textFormat{ed: ed, rev: ed.Revision(), eol: detectEOL(text), enc: detectEncoding(text)}
	return a.footerFmt
}

// detectEOL is the first line's ending; a file with one line is LF.
func detectEOL(text []byte) string {
	if i := bytes.IndexByte(text, '\n'); i > 0 && text[i-1] == '\r' {
		return eolCRLF
	}
	return eolLF
}

func detectEncoding(text []byte) string {
	switch {
	case bytes.HasPrefix(text, bomUTF8):
		return encUTF8BOM
	case bytes.HasPrefix(text, []byte{0xFF, 0xFE}):
		return encUTF16LE
	case bytes.HasPrefix(text, []byte{0xFE, 0xFF}):
		return encUTF16BE
	case !utf8.Valid(text):
		return encBinary
	}
	return encUTF8
}

// withEOL is text with every line ending made eol.
func withEOL(text []byte, eol string) []byte {
	lf := bytes.ReplaceAll(text, []byte("\r\n"), []byte("\n"))
	if eol == eolCRLF {
		return bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	}
	return lf
}

// setEOL rewrites ed's line endings as one undoable edit.
func (a *App) setEOL(ed *codeeditor.Editor, eol string) {
	ed.ApplyText(string(withEOL(ed.Bytes(), eol)))
}

// setBOM adds or removes the UTF-8 byte order mark at the start of ed.
func (a *App) setBOM(ed *codeeditor.Editor, on bool) {
	text := ed.Bytes()
	has := bytes.HasPrefix(text, bomUTF8)
	switch {
	case on && !has:
		ed.ApplyText(string(bomUTF8) + string(text))
	case !on && has:
		ed.ApplyText(string(text[len(bomUTF8):]))
	}
}

func (a *App) eolButton(ed *codeeditor.Editor) ui.View {
	return footerMenu("ft-eol", a.formatOf(ed).eol, a.eolMenu(ed)).Tooltip("Line endings")
}

// eolMenu converts ed's line endings.
func (a *App) eolMenu(ed *codeeditor.Editor) []ui.MenuItem {
	cur := a.formatOf(ed).eol
	item := func(eol, label string) ui.MenuItem {
		return ui.MenuItem{Label: label, Checked: cur == eol, OnSelect: func() { a.setEOL(ed, eol) }}
	}
	return []ui.MenuItem{
		{Label: "Line endings", Disabled: true},
		item(eolLF, "LF (\\n)"),
		item(eolCRLF, "CRLF (\\r\\n)"),
	}
}

func (a *App) encodingButton(ed *codeeditor.Editor) ui.View {
	return footerMenu("ft-encoding", a.formatOf(ed).enc, a.encodingMenu(ed)).Tooltip("Encoding")
}

// encodingMenu adds or removes a UTF-8 file's byte order mark; hm reads
// other encodings as they are, without converting them.
func (a *App) encodingMenu(ed *codeeditor.Editor) []ui.MenuItem {
	cur := a.formatOf(ed).enc
	if cur != encUTF8 && cur != encUTF8BOM {
		return []ui.MenuItem{{Label: cur + ": hm edits UTF-8 text only", Disabled: true}}
	}
	return []ui.MenuItem{
		{Label: "Encoding", Disabled: true},
		{Label: encUTF8, Checked: cur == encUTF8, OnSelect: func() { a.setBOM(ed, false) }},
		{Label: encUTF8BOM, Checked: cur == encUTF8BOM, OnSelect: func() { a.setBOM(ed, true) }},
	}
}

// indentOverride is a file's indentation picked in the footer, kept over
// the config for the rest of the session.
type indentOverride struct {
	spaces bool
	width  int
}

func (a *App) indentButton(ed *codeeditor.Editor, path string) ui.View {
	label := fmt.Sprintf("Tab Size: %d", ed.TabWidth())
	if ed.IndentSpaces() {
		label = fmt.Sprintf("Spaces: %d", ed.TabWidth())
	}
	return footerMenu("ft-indent", label, a.indentMenu(ed, path)).Tooltip("Indentation for this file")
}

// indentMenu sets path's indentation for the session.
func (a *App) indentMenu(ed *codeeditor.Editor, path string) []ui.MenuItem {
	spaces, width := ed.IndentSpaces(), ed.TabWidth()
	set := func(spaces bool, width int) {
		if a.indents == nil {
			a.indents = map[string]indentOverride{}
		}
		a.indents[path] = indentOverride{spaces, width}
		a.applyAllEditorSettings()
	}
	items := []ui.MenuItem{
		{Label: "Indent Using Spaces", Checked: spaces, OnSelect: func() { set(true, width) }},
		{Label: "Indent Using Tabs", Checked: !spaces, OnSelect: func() { set(false, width) }},
		ui.MenuSeparator,
	}
	for _, w := range []int{2, 4, 8} {
		items = append(items, ui.MenuItem{Label: fmt.Sprintf("Width %d", w), Checked: w == width,
			OnSelect: func() { set(spaces, w) }})
	}
	if _, ok := a.indents[path]; ok {
		items = append(items, ui.MenuSeparator, ui.MenuItem{Label: "Use Settings", Icon: icons.RotateCcw,
			OnSelect: func() {
				delete(a.indents, path)
				a.applyAllEditorSettings()
			}})
	}
	return items
}
