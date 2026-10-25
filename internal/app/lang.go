package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/lang"
)

// requestTimeout bounds navigation requests to a language server.
const requestTimeout = 15 * time.Second

// onUI runs fn on the UI goroutine at the next frame. Safe from any goroutine.
func (a *App) onUI(fn func()) {
	a.uiq <- fn
	a.invalidate()
}

// drainUI runs queued UI work; call at the top of Body.
func (a *App) drainUI() {
	for {
		select {
		case fn := <-a.uiq:
			fn()
		default:
			return
		}
	}
}

// reportLanguages turns language problems into notifications.
func (a *App) reportLanguages() {
	for _, n := range a.lang.Drain() {
		kind := ui.ToastError
		if n.Warning {
			kind = ui.ToastWarning
		}
		a.notifs.Add(n.Title+": "+n.Message, kind)
		if !n.Toast || a.ctx == nil {
			continue
		}
		opts := ui.ToastOpts{ID: "lang-" + n.Title, Title: n.Title, Message: n.Message, Variant: kind, Duration: 12 * time.Second}
		if m := n.Missing; m != nil {
			if inst, ok := lang.PickInstaller(m.Install); ok {
				opts.Actions = []ui.ToastAction{{Label: "Install with " + inst.Tool, Primary: true, OnClick: func() {
					a.install(m, inst)
				}}}
				opts.Message += " Install it with: " + lang.InstallerString(inst)
			} else if len(m.Install) > 0 {
				opts.Message += " Install it with: " + lang.InstallerString(m.Install[0])
			}
		}
		a.ctx.Toasts().Notify(opts)
	}
}

// install runs an installer in the background and restarts servers after.
func (a *App) install(m *lang.Missing, inst config.Installer) {
	if a.installing[m.Command] {
		return
	}
	a.installing[m.Command] = true
	a.toast("Installing "+m.Command+"…", ui.ToastInfo)
	go func() {
		var out strings.Builder
		err := lang.Install(inst, m.Command, func(line string) { out.WriteString(line + "\n") })
		a.onUI(func() {
			delete(a.installing, m.Command)
			a.appendOutput(out.String())
			if err != nil {
				a.toast("Install of "+m.Command+" failed: "+err.Error(), ui.ToastError)
				return
			}
			a.toast(m.Command+" installed", ui.ToastSuccess)
			a.lang.Restart()
		})
	}()
}

// appendOutput adds text to the Output panel.
func (a *App) appendOutput(s string) {
	if s == "" {
		return
	}
	a.output.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		a.output.WriteString("\n")
	}
	a.outputDirty = true
}

// position is the LSP position of the active editor's caret.
func (a *App) position(ed *ui.Editor, off int) lsp.Position {
	return lsp.PositionAt(ed.Bytes(), off, a.lang.Manager().Encoding(ed.Path))
}

// goTo opens loc and selects its range. enc is the encoding of the server
// that produced it.
func (a *App) goTo(loc lsp.Location, enc string) {
	path := loc.Path()
	a.openFile(path)
	ed := a.bufs.Get(path)
	if ed == nil {
		return
	}
	text := ed.Bytes()
	lo, hi := lsp.OffsetAt(text, loc.Range.Start, enc), lsp.OffsetAt(text, loc.Range.End, enc)
	ed.SetSelection(lo, hi)
	ed.Focus()
}

// definition jumps to the definition of the symbol at off.
func (a *App) definition(ed *ui.Editor, off int) {
	a.navigate(ed, off, "definition", func(ctx context.Context, m *lsp.Manager, path string, pos lsp.Position) ([]lsp.Location, error) {
		return m.Definition(ctx, path, pos)
	})
}

func (a *App) references(ed *ui.Editor, off int) {
	a.navigate(ed, off, "references", func(ctx context.Context, m *lsp.Manager, path string, pos lsp.Position) ([]lsp.Location, error) {
		return m.References(ctx, path, pos, true)
	})
}

func (a *App) implementation(ed *ui.Editor, off int) {
	a.navigate(ed, off, "implementations", func(ctx context.Context, m *lsp.Manager, path string, pos lsp.Position) ([]lsp.Location, error) {
		return m.Implementation(ctx, path, pos)
	})
}

type locator func(ctx context.Context, m *lsp.Manager, path string, pos lsp.Position) ([]lsp.Location, error)

// navigate asks the server for locations; one is opened, several are listed.
func (a *App) navigate(ed *ui.Editor, off int, what string, find locator) {
	if ed == nil {
		return
	}
	path, pos := ed.Path, a.position(ed, off)
	m := a.lang.Manager()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		locs, err := find(ctx, m, path, pos)
		enc := m.Encoding(path)
		a.onUI(func() {
			switch {
			case err != nil:
				a.lspFailed("Could not find "+what, err)
			case len(locs) == 0:
				a.toast("No "+what+" found", ui.ToastInfo)
			case len(locs) == 1:
				a.goTo(locs[0], enc)
			default:
				a.showLocations(what, locs, enc)
			}
		})
	}()
}

// lspFailed reports a failed request, explaining the routine reasons.
func (a *App) lspFailed(prefix string, err error) {
	switch {
	case errors.Is(err, lsp.ErrNoServer), errors.Is(err, lsp.ErrNoDocument):
		a.toast(prefix+": no language server for this file", ui.ToastWarning)
	case errors.Is(err, lsp.ErrUnsupported):
		a.toast(prefix+": the language server does not support it", ui.ToastWarning)
	default:
		a.fail(prefix, err)
	}
}

// showLocations lists locations in the palette.
func (a *App) showLocations(what string, locs []lsp.Location, enc string) {
	a.locations = nil
	for i, l := range locs {
		l := l
		path := l.Path()
		line := l.Range.Start.Line + 1
		preview := lineText(a.bufs.Get(path), path, l.Range.Start.Line)
		a.locations = append(a.locations, ui.Item(fmt.Sprintf("loc.%d", i)).
			Title(strings.TrimSpace(preview)).
			Detail(fmt.Sprintf("%s:%d", a.ws.Rel(path), line)).
			Icon(icons.FileCode).Scope(scopeLocations).
			Run(func() { a.goTo(l, enc) }))
	}
	a.ctx.Commands().Register(a.locations...)
	a.ctx.Commands().ShowScope(scopeLocations, fmt.Sprintf("%d %s", len(locs), what))
}

// lineText reads one line of a buffer, or of the file when it is not open.
func lineText(ed *ui.Editor, path string, line int) string {
	var text []byte
	if ed != nil {
		text = ed.Bytes()
	} else if data, err := os.ReadFile(path); err == nil {
		text = data
	}
	lines := strings.SplitN(string(text), "\n", line+2)
	if line < len(lines) {
		return lines[line]
	}
	return filepath.Base(path)
}

// rename asks for a new name and renames the symbol at the caret everywhere.
func (a *App) rename(ed *ui.Editor) {
	if ed == nil {
		return
	}
	off := ed.Caret()
	lo, hi := wordAt(ed.Bytes(), off)
	old := string(ed.Bytes()[lo:hi])
	a.ctx.Dialogs().ShowInputValue("Rename symbol", "New name", old, func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || name == old {
			return
		}
		path, pos, m := ed.Path, a.position(ed, off), a.lang.Manager()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
			defer cancel()
			we, err := m.Rename(ctx, path, pos, name)
			enc := m.Encoding(path)
			a.onUI(func() {
				if err != nil {
					a.lspFailed("Rename failed", err)
					return
				}
				n, err := a.applyWorkspaceEdit(we, enc)
				if err != nil {
					a.fail("Rename failed", err)
					return
				}
				a.toast(fmt.Sprintf("Renamed %s to %s in %d %s", old, name, n, plural(n, "file")), ui.ToastSuccess)
			})
		}()
	}, nil)
}

// applyWorkspaceEdit applies edits to the files' buffers, opening tabs for
// files that were not open so the changes can be reviewed and saved.
func (a *App) applyWorkspaceEdit(we lsp.WorkspaceEdit, enc string) (int, error) {
	edits := we.Edits()
	for path, es := range edits {
		if _, err := a.ws.Open(path); err != nil {
			return 0, err
		}
		ed := a.bufs.Get(path)
		out, err := lsp.ApplyEdits(ed.Bytes(), es, enc)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", a.ws.Rel(path), err)
		}
		ed.ApplyText(string(out))
	}
	return len(edits), nil
}

// serverApplyEdit answers a server's workspace/applyEdit from its goroutine.
func (a *App) serverApplyEdit(we lsp.WorkspaceEdit, enc string) bool {
	done := make(chan bool, 1)
	a.onUI(func() {
		_, err := a.applyWorkspaceEdit(we, enc)
		a.fail("Could not apply the server's edit", err)
		done <- err == nil
	})
	select {
	case ok := <-done:
		return ok
	case <-time.After(10 * time.Second):
		return false
	}
}

// documentSymbols lists the active file's symbols in the palette.
func (a *App) documentSymbols(ed *ui.Editor) {
	if ed == nil {
		return
	}
	path, m := ed.Path, a.lang.Manager()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		syms, err := m.DocumentSymbols(ctx, path)
		enc := m.Encoding(path)
		a.onUI(func() {
			if err != nil {
				a.lspFailed("Could not list symbols", err)
				return
			}
			var locs []*ui.Command
			var walk func(ss []lsp.DocumentSymbol, depth int)
			walk = func(ss []lsp.DocumentSymbol, depth int) {
				for _, s := range ss {
					loc := lsp.Location{URI: lsp.PathToURI(path), Range: s.SelectionRange}
					locs = append(locs, ui.Item(fmt.Sprintf("sym.%d", len(locs))).
						Title(strings.Repeat("  ", depth)+s.Name).Detail(s.Detail).
						Trailing(symbolKind(s.Kind)).Scope(scopeLocations).
						Run(func() { a.goTo(loc, enc) }))
					walk(s.Children, depth+1)
				}
			}
			walk(syms, 0)
			a.locations = locs
			a.ctx.Commands().Register(locs...)
			a.ctx.Commands().ShowScope(scopeLocations, "Go to symbol in "+filepath.Base(path))
		})
	}()
}

var symbolKinds = []string{"", "file", "module", "namespace", "package", "class", "method", "property",
	"field", "constructor", "enum", "interface", "function", "variable", "constant", "string",
	"number", "boolean", "array", "object", "key", "null", "enum member", "struct", "event",
	"operator", "type parameter"}

func symbolKind(k lsp.SymbolKind) string {
	if int(k) > 0 && int(k) < len(symbolKinds) {
		return symbolKinds[k]
	}
	return ""
}

// wordAt returns the identifier around off.
func wordAt(text []byte, off int) (int, int) {
	isWord := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	lo, hi := off, off
	for lo > 0 && isWord(text[lo-1]) {
		lo--
	}
	for hi < len(text) && isWord(text[hi]) {
		hi++
	}
	return lo, hi
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
