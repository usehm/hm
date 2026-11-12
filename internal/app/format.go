package app

import (
	"context"
	"errors"
	"time"

	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/lang"
)

// syncFormatTimeout bounds formatting that blocks the UI, before a tab
// closes or a folder switch.
const syncFormatTimeout = 5 * time.Second

// formatRequest builds the formatter run for a buffer, or reports false
// when its language formats nothing on trigger.
func (a *App) formatRequest(ed *ui.Editor, trigger string) (lang.FormatRequest, bool) {
	l, ok := a.lang.For(ed.Path)
	if !ok {
		return lang.FormatRequest{}, false
	}
	req := lang.FormatRequest{Path: ed.Path, Src: ed.Bytes(), Trigger: trigger, Formatters: l.Formatters}
	if len(l.Formatters) == 0 && trigger != "paste" {
		m, path := a.lang.Manager(), ed.Path
		if !m.Supports(path, "documentFormattingProvider") {
			return req, false
		}
		edCfg := a.editorSettings(path)
		opts := lsp.FormattingOptions{TabSize: edCfg.TabWidth, InsertSpaces: edCfg.InsertSpaces}
		req.LSP = func(ctx context.Context, src []byte) ([]byte, error) {
			edits, err := m.Format(ctx, path, opts)
			if err != nil {
				return nil, err
			}
			return lsp.ApplyEdits(src, edits, m.Encoding(path))
		}
	}
	return req, true
}

// format runs the chain for trigger on ed in the background. done, when set,
// runs on the UI goroutine afterwards, whether or not formatting worked.
func (a *App) format(ed *ui.Editor, trigger string, done func()) {
	req, ok := a.formatRequest(ed, trigger)
	if !ok {
		if trigger == "manual" {
			a.toast("No formatter is configured for this file", ui.ToastInfo)
		}
		if done != nil {
			done()
		}
		return
	}
	rev := ed.Revision()
	a.formatting[ed.Path]++
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out, err := lang.Format(ctx, req)
		a.onUI(func() {
			if a.formatting[req.Path]--; a.formatting[req.Path] <= 0 {
				delete(a.formatting, req.Path)
			}
			a.finishFormat(ed, rev, out, err)
			if done != nil {
				done()
			}
		})
	}()
}

// formatNow is format on the UI goroutine, for flows that close the buffer
// right after.
func (a *App) formatNow(ed *ui.Editor, trigger string) {
	req, ok := a.formatRequest(ed, trigger)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncFormatTimeout)
	defer cancel()
	out, err := lang.Format(ctx, req)
	a.finishFormat(ed, ed.Revision(), out, err)
}

// finishFormat applies a formatter's output, unless the buffer changed
// while it ran or the formatter failed.
func (a *App) finishFormat(ed *ui.Editor, rev uint64, out []byte, err error) {
	var missing *lang.MissingError
	switch {
	case errors.Is(err, lang.ErrNothingToRun):
	case errors.As(err, &missing):
		a.lang.Notify(lang.Notice{
			Title: missing.Command + " not installed", Warning: true,
			Message: "Formatting was skipped.",
			Missing: &lang.Missing{Command: missing.Command, Install: missing.Install},
		})
	case err != nil:
		a.appendOutput(err.Error())
		a.notifs.Add("Format failed: "+err.Error(), ui.ToastError, a.openFileAction(ed.Path),
			notifAction{label: "Show Output", run: func() { a.showPanel(panelOutput) }})
		if a.ctx != nil {
			a.ctx.Toasts().Notify(ui.ToastOpts{
				ID: "format-failed", Title: "Format failed", Message: firstLine(err.Error()),
				Variant: ui.ToastError, Duration: 8 * time.Second,
				Actions: []ui.ToastAction{{Label: "Show output", OnClick: func() { a.showPanel(panelOutput) }}},
			})
		}
	case ed.Revision() != rev:
		a.notifs.Add("Formatting skipped: "+a.ws.Rel(ed.Path)+" changed while it ran", ui.ToastInfo)
	default:
		ed.ApplyText(string(out))
	}
}

// saveFile writes path, formatting it first when format_on_save is on.
// Saving a config file applies it.
func (a *App) saveFile(path string) error {
	ed := a.bufs.Get(path)
	if ed == nil {
		return nil
	}
	if a.editorSettings(path).FormatOnSave && !a.isConfigPath(path) {
		if _, ok := a.formatRequest(ed, "save"); ok {
			a.format(ed, "save", func() {
				if a.bufs.Get(path) == ed {
					a.fail("Save failed", a.writeFile(path))
				}
			})
			return nil
		}
	}
	return a.writeFile(path)
}

// saveFileNow is saveFile that has written the file when it returns.
func (a *App) saveFileNow(path string) error {
	ed := a.bufs.Get(path)
	if ed == nil {
		return nil
	}
	if a.editorSettings(path).FormatOnSave && !a.isConfigPath(path) {
		a.formatNow(ed, "save")
	}
	return a.writeFile(path)
}

func (a *App) writeFile(path string) error {
	if ed := a.bufs.Get(path); ed != nil {
		a.trimTrailingWhitespace(ed)
	}
	if err := a.bufs.Save(path); err != nil {
		return err
	}
	if a.isConfigPath(path) {
		a.reloadConfig()
	}
	return nil
}

func (a *App) isConfigPath(path string) bool {
	user, project := a.cfg.Paths()
	return path == user || path == project
}

// formatAfterPaste runs paste formatters once a paste has landed.
func (a *App) formatAfterPaste() {
	p := a.pasted
	if p == nil {
		return
	}
	ed := a.bufs.Get(p.path)
	if ed == nil || ed.Revision() == p.rev {
		if time.Since(p.at) > time.Second {
			a.pasted = nil
		}
		return
	}
	a.pasted = nil
	if req, ok := a.formatRequest(ed, "paste"); ok && len(req.Formatters) > 0 {
		a.format(ed, "paste", nil)
	}
}

type pasteMark struct {
	path string
	rev  uint64
	at   time.Time
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}
