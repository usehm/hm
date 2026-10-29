package app

import (
	"maps"
	"slices"
	"strconv"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/config"
)

// editorSection is Settings › Editor. The language picker switches the form
// to one language's overrides, written under languages.<name>.editor.
func (a *App) editorSection(c *ui.Ctx) ui.View {
	th := c.Theme()
	cfg := a.cfg.Current()
	lang := a.settingsLang
	if _, ok := cfg.Languages[lang]; !ok {
		lang = ""
	}
	opts := []ui.SelectOption{{Label: "All languages", Value: ""}}
	sel := 0
	for i, name := range slices.Sorted(maps.Keys(cfg.Languages)) {
		opts = append(opts, ui.SelectOption{Label: name, Value: name})
		if name == lang {
			sel = i + 1
		}
	}
	picker := ui.Form("settings-editor-lang",
		ui.FormSelect("set-editor-lang", "Language", "A language's settings override the ones for all languages",
			opts, sel, func(v string) { a.settingsLang = v }),
	).Grouped()
	if lang == "" {
		return ui.Column(picker, a.editorForm(cfg.Editor)).Gap(th.Spacing.L)
	}
	return ui.Column(picker, a.languageEditorForm(c, lang, cfg)).Gap(th.Spacing.L)
}

// editorForm edits the editor section, for all languages.
func (a *App) editorForm(ed config.Editor) ui.View {
	key := func(k ...string) []string { return k }
	return ui.Form("settings-editor",
		ui.FormHeading("Indentation", ""),
		ui.FormNumber("set-tab-width", "Tab width", "Columns per tab stop",
			float64(ed.TabWidth), 1, 16, 1, func(v float64) { a.setSetting(key("editor", "tab_width"), int(v)) }),
		ui.FormSwitch("set-spaces", "Insert spaces", "Tab inserts spaces instead of a tab",
			ed.InsertSpaces, func(v bool) { a.setSetting(key("editor", "insert_spaces"), v) }),
		ui.FormSelect("set-auto-indent", "Auto indent", "keep copies the line's indent; advanced adds a level after an opening bracket",
			enumOptions("editor.auto_indent"), enumIndex("editor.auto_indent", ed.AutoIndent),
			func(v string) { a.setSetting(key("editor", "auto_indent"), v) }),
		ui.FormHeading("Display", ""),
		ui.FormSwitch("set-wrap", "Soft wrap", "Wrap long lines at the window edge",
			ed.SoftWrap, func(v bool) { a.setSetting(key("editor", "soft_wrap"), v) }),
		ui.FormSwitch("set-lines", "Line numbers", "Show the line-number gutter",
			ed.LineNumbers, func(v bool) { a.setSetting(key("editor", "line_numbers"), v) }),
		ui.FormSwitch("set-past-end", "Scroll past end", "Leave room below the last line",
			ed.ScrollPastEnd, func(v bool) { a.setSetting(key("editor", "scroll_past_end"), v) }),
		ui.FormHeading("Input", ""),
		ui.FormSelect("set-keymap", "Keymap", "vim for Vim's modal editing",
			enumOptions("editor.keymap"), enumIndex("editor.keymap", ed.Keymap),
			func(v string) { a.setSetting(key("editor", "keymap"), v) }),
	).Grouped()
}

// languageEditorForm edits lang's overrides. Each row shows the value lang
// gets, and says when it differs from the setting for all languages.
func (a *App) languageEditorForm(c *ui.Ctx, lang string, cfg config.Config) ui.View {
	base := cfg.Editor
	o := cfg.Languages[lang].Editor
	if o == nil {
		o = &config.EditorOverride{}
	}
	es := base.For(o)
	key := func(k string) []string { return []string{"languages", lang, "editor", k} }
	desc := func(what string, set bool, all string) string {
		if !set {
			return what
		}
		return what + ". Set for " + lang + "; all languages: " + all
	}
	onOff := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	form := ui.Form("settings-editor-"+lang,
		ui.FormHeading("Indentation", ""),
		ui.FormNumber("set-lang-tab-width", "Tab width", desc("Columns per tab stop", o.TabWidth != nil, strconv.Itoa(base.TabWidth)),
			float64(es.TabWidth), 1, 16, 1, func(v float64) { a.setSetting(key("tab_width"), int(v)) }),
		ui.FormSwitch("set-lang-spaces", "Insert spaces", desc("Tab inserts spaces instead of a tab", o.InsertSpaces != nil, onOff(base.InsertSpaces)),
			es.InsertSpaces, func(v bool) { a.setSetting(key("insert_spaces"), v) }),
		ui.FormSelect("set-lang-auto-indent", "Auto indent", desc("What Enter carries to the new line", o.AutoIndent != nil, base.AutoIndent),
			enumOptions("editor.auto_indent"), enumIndex("editor.auto_indent", es.AutoIndent),
			func(v string) { a.setSetting(key("auto_indent"), v) }),
		ui.FormHeading("Display", ""),
		ui.FormSwitch("set-lang-wrap", "Soft wrap", desc("Wrap long lines at the window edge", o.SoftWrap != nil, onOff(base.SoftWrap)),
			es.SoftWrap, func(v bool) { a.setSetting(key("soft_wrap"), v) }),
		ui.FormHeading("Saving", ""),
		ui.FormSwitch("set-lang-format", "Format on save", desc("Run the language's formatters when saving", o.FormatOnSave != nil, onOff(base.FormatOnSave)),
			es.FormatOnSave, func(v bool) { a.setSetting(key("format_on_save"), v) }),
		ui.FormSwitch("set-lang-trim", "Trim trailing whitespace", desc("Strip blanks at line ends when saving", o.TrimTrailingWhitespace != nil, onOff(base.TrimTrailingWhitespace)),
			es.TrimTrailingWhitespace, func(v bool) { a.setSetting(key("trim_trailing_whitespace"), v) }),
	).Grouped()
	reset := ui.Button("set-lang-reset", ui.Text("Reset "+lang)).IconStart(icons.RotateCcw).
		Tooltip("Remove " + lang + "'s editor settings from this file; built-in ones still apply").
		OnClick(func() { a.unsetSetting([]string{"languages", lang, "editor"}) })
	return ui.Column(form, ui.Row(ui.Spacer(), reset)).Gap(c.Theme().Spacing.XS)
}
