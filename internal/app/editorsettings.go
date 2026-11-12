package app

import (
	"bytes"

	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/config"
)

var autoIndentModes = map[string]ui.AutoIndent{
	config.AutoIndentNone:     ui.AutoIndentNone,
	config.AutoIndentKeep:     ui.AutoIndentKeep,
	config.AutoIndentAdvanced: ui.AutoIndentBrackets,
}

// editorSettings is the editor section with path's language overrides.
func (a *App) editorSettings(path string) config.Editor {
	ed := a.cfg.Current().Editor
	if l, ok := a.lang.For(path); ok {
		return ed.For(l.Editor)
	}
	return ed
}

// applyEditorSettings gives ed, an editor of path, its language's
// indentation. Soft wrap is only set when the language overrides it, so a
// wrap toggled by hand survives a config reload otherwise.
func (a *App) applyEditorSettings(ed *ui.Editor, path string) {
	es := a.editorSettings(path)
	ed.SetTabWidth(es.TabWidth)
	ed.SetIndentSpaces(es.InsertSpaces)
	ed.SetAutoIndent(autoIndentModes[es.AutoIndent])
	if l, ok := a.lang.For(path); ok && l.Editor != nil && l.Editor.SoftWrap != nil {
		ed.SetSoftWrap(*l.Editor.SoftWrap)
	}
}

// applyAllEditorSettings reapplies editor settings to every open editor.
func (a *App) applyAllEditorSettings() {
	if a.bufs == nil {
		return
	}
	for _, ed := range a.bufs.All() {
		a.applyEditorSettings(ed, ed.Path)
	}
	for _, g := range a.groups {
		for p, m := range g.mirrors {
			a.applyEditorSettings(m.ed, p)
		}
	}
}

// trimTrailingWhitespace strips blanks at the end of every line of ed,
// when its language asks for it.
func (a *App) trimTrailingWhitespace(ed *ui.Editor) {
	if !a.editorSettings(ed.Path).TrimTrailingWhitespace {
		return
	}
	lines := bytes.Split(ed.Bytes(), []byte("\n"))
	for i, ln := range lines {
		lines[i] = bytes.TrimRight(ln, " \t")
	}
	ed.ApplyText(string(bytes.Join(lines, []byte("\n"))))
}
