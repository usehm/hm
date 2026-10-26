package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent/tools"
	"github.com/mirzakhany/hm/internal/lang"
)

// agentCode gives the agent the editor's language servers and formatters.
type agentCode struct{ a *App }

// attach makes sure path has a buffer attached to its language server, and
// returns the server position of line:col (1-based) with the text.
func (c agentCode) attach(path string, line, col int) (lsp.Position, []byte, error) {
	var pos lsp.Position
	var text []byte
	var err error
	cerr := c.a.callUI(func() {
		var ed *ui.Editor
		if ed, err = c.a.agentBuffer(path); err != nil {
			return
		}
		if !c.a.lang.HasServer(path) {
			err = errors.New("no language server is configured for " + c.a.ws.Rel(path))
			return
		}
		ed.AttachLSP()
		text = append([]byte(nil), ed.Bytes()...)
		off := ed.Offset(max(line-1, 0), max(col-1, 0))
		pos = lsp.PositionAt(text, off, c.a.lang.Manager().Encoding(path))
	})
	if cerr != nil {
		return pos, nil, cerr
	}
	return pos, text, err
}

// retry repeats a request while the server is still starting.
func retry[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	deadline := time.Now().Add(20 * time.Second)
	for {
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		v, err := fn(rctx)
		cancel()
		if err == nil || ctx.Err() != nil || time.Now().After(deadline) ||
			!(errors.Is(err, lsp.ErrNoServer) || errors.Is(err, context.DeadlineExceeded)) {
			return v, err
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (c agentCode) locations(ctx context.Context, path string, line, col int,
	find func(context.Context, *lsp.Manager, lsp.Position) ([]lsp.Location, error)) ([]tools.Location, error) {
	pos, _, err := c.attach(path, line, col)
	if err != nil {
		return nil, err
	}
	m := c.a.lang.Manager()
	locs, err := retry(ctx, func(ctx context.Context) ([]lsp.Location, error) { return find(ctx, m, pos) })
	if err != nil {
		return nil, err
	}
	enc := m.Encoding(path)
	out := make([]tools.Location, 0, len(locs))
	for _, l := range locs {
		p := l.Path()
		text := c.text(p)
		off := lsp.OffsetAt(text, l.Range.Start, enc)
		ln, cl := lineCol(text, off)
		out = append(out, tools.Location{Path: p, Line: ln + 1, Col: cl + 1, Text: lineAt(text, ln)})
	}
	return out, nil
}

// text reads a file through its buffer when one is open.
func (c agentCode) text(path string) []byte {
	var data []byte
	_ = c.a.callUI(func() {
		if ed := c.a.bufs.Get(path); ed != nil {
			data = append([]byte(nil), ed.Bytes()...)
		}
	})
	if data == nil {
		data, _ = os.ReadFile(path)
	}
	return data
}

func (c agentCode) Definition(ctx context.Context, path string, line, col int) ([]tools.Location, error) {
	return c.locations(ctx, path, line, col, func(ctx context.Context, m *lsp.Manager, pos lsp.Position) ([]lsp.Location, error) {
		return m.Definition(ctx, path, pos)
	})
}

func (c agentCode) References(ctx context.Context, path string, line, col int) ([]tools.Location, error) {
	return c.locations(ctx, path, line, col, func(ctx context.Context, m *lsp.Manager, pos lsp.Position) ([]lsp.Location, error) {
		return m.References(ctx, path, pos, true)
	})
}

func (c agentCode) Hover(ctx context.Context, path string, line, col int) (string, error) {
	pos, _, err := c.attach(path, line, col)
	if err != nil {
		return "", err
	}
	m := c.a.lang.Manager()
	return retry(ctx, func(ctx context.Context) (string, error) { return m.HoverText(ctx, path, pos) })
}

func (c agentCode) Rename(ctx context.Context, path string, line, col int, newName string) ([]tools.FileEdit, error) {
	pos, _, err := c.attach(path, line, col)
	if err != nil {
		return nil, err
	}
	m := c.a.lang.Manager()
	we, err := retry(ctx, func(ctx context.Context) (lsp.WorkspaceEdit, error) { return m.Rename(ctx, path, pos, newName) })
	if err != nil {
		return nil, err
	}
	enc := m.Encoding(path)
	var out []tools.FileEdit
	for p, edits := range we.Edits() {
		old := c.text(p)
		next, err := lsp.ApplyEdits(old, edits, enc)
		if err != nil {
			return nil, err
		}
		out = append(out, tools.FileEdit{Path: p, Old: old, New: next})
	}
	return out, nil
}

func (c agentCode) Format(ctx context.Context, path string) ([]byte, error) {
	var req lang.FormatRequest
	var ok bool
	var err error
	cerr := c.a.callUI(func() {
		var ed *ui.Editor
		if ed, err = c.a.agentBuffer(path); err != nil {
			return
		}
		ed.AttachLSP()
		req, ok = c.a.formatRequest(ed, "manual")
	})
	if cerr != nil {
		return nil, cerr
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("no formatter is configured for " + c.a.ws.Rel(path))
	}
	return lang.Format(ctx, req)
}

func lineCol(text []byte, off int) (int, int) {
	off = max(0, min(off, len(text)))
	line, start := 0, 0
	for i := 0; i < off; i++ {
		if text[i] == '\n' {
			line++
			start = i + 1
		}
	}
	return line, len([]rune(string(text[start:off])))
}

func lineAt(text []byte, line int) string {
	lines := strings.SplitN(string(text), "\n", line+2)
	if line < len(lines) {
		return lines[line]
	}
	return ""
}

// agentBuffer returns path's buffer, opening one (without a tab) for the
// language server; it is released when the turn ends unless edited.
func (a *App) agentBuffer(path string) (*ui.Editor, error) {
	if ed := a.bufs.Get(path); ed != nil {
		return ed, nil
	}
	ed, err := a.bufs.Open(path)
	if err != nil {
		return nil, err
	}
	if a.agent.borrowed == nil {
		a.agent.borrowed = map[string]bool{}
	}
	a.agent.borrowed[path] = true
	return ed, nil
}

// releaseAgentBuffers closes buffers the agent opened that have no tab;
// edited ones get a tab instead.
func (a *App) releaseAgentBuffers() {
	for p := range a.agent.borrowed {
		if a.hasTab(p) || (a.search.open && a.search.preview == p) {
			continue
		}
		if ed := a.bufs.Get(p); ed != nil && ed.Modified() {
			_, _ = a.ws.Open(p)
		} else {
			a.bufs.Close(p)
		}
	}
	a.agent.borrowed = nil
}
