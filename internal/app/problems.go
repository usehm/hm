package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mirzakhany/yoga/highlight"
	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/ui"
)

// problem is one diagnostic in an open file.
type problem struct {
	path string
	diag lsp.Diagnostic
	enc  string
}

// problems collects diagnostics from every open file, errors first.
func (a *App) problems() []problem {
	var out []problem
	for _, t := range a.ws.Tabs {
		ed := a.bufs.Get(t.Path)
		if ed == nil {
			continue
		}
		for _, d := range ed.Diagnostics() {
			out = append(out, problem{path: t.Path, diag: d, enc: ed.LSPEncoding()})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		if si, sj := severity(out[i].diag), severity(out[j].diag); si != sj {
			return si < sj
		}
		return out[i].diag.Range.Start.Line < out[j].diag.Range.Start.Line
	})
	return out
}

// severity treats a missing severity as an error, as the spec says.
func severity(d lsp.Diagnostic) lsp.DiagnosticSeverity {
	if d.Severity == 0 {
		return lsp.SeverityError
	}
	return d.Severity
}

func countProblems(ps []problem) (errs, warns int) {
	for _, p := range ps {
		switch severity(p.diag) {
		case lsp.SeverityError:
			errs++
		case lsp.SeverityWarning:
			warns++
		}
	}
	return
}

func (a *App) problemsView(c *ui.Ctx) ui.View {
	th := c.Theme()
	ps := a.problems()
	if len(ps) == 0 {
		return ui.EmptyState("No problems", "Diagnostics from language servers in open files show here.").EmptyIcon(icons.CircleCheck)
	}
	rows := []ui.View{}
	last := ""
	for i, p := range ps {
		p := p
		if p.path != last {
			last = p.path
			rows = append(rows, ui.Row(
				ui.Icon(icons.FileCode, 14, th.ForegroundMuted),
				ui.Strong(filepath.Base(p.path)),
				ui.Caption(a.ws.Rel(filepath.Dir(p.path))).Ellipsis(ui.EllipsisMiddle),
			).Gap(th.Spacing.XS).PaddingXY(th.Spacing.S, th.Spacing.XXS))
		}
		icon, color := icons.CircleX, th.ErrorForeground
		switch severity(p.diag) {
		case lsp.SeverityWarning:
			icon, color = icons.TriangleAlert, th.WarningForeground
		case lsp.SeverityInformation, lsp.SeverityHint:
			icon, color = icons.Info, th.ForegroundMuted
		}
		src := ""
		if p.diag.Source != "" {
			src = p.diag.Source + " · "
		}
		rows = append(rows, ui.Button(fmt.Sprintf("problem-%d", i),
			ui.Row(
				ui.Icon(icon, 14, color),
				ui.Text(firstLine(p.diag.Message)).Ellipsis(ui.EllipsisEnd).Grow(1),
				ui.Caption(fmt.Sprintf("%s%d:%d", src, p.diag.Range.Start.Line+1, p.diag.Range.Start.Character+1)),
			).Gap(th.Spacing.S).Grow(1),
		).Ghost().HoverFill().Tooltip(p.diag.Message).
			OnClick(func() { a.goTo(lsp.Location{URI: lsp.PathToURI(p.path), Range: p.diag.Range}, p.enc) }).
			PaddingLeft(th.Spacing.XL))
	}
	return ui.Scroll("problems-scroll", ui.Column(rows...).Gap(th.Spacing.XXS).PaddingXY(0, th.Spacing.XS)).Grow(1)
}

// outputView shows formatter, installer and task output read-only.
func (a *App) outputView(c *ui.Ctx) ui.View {
	if a.output.Len() == 0 {
		return ui.EmptyState("No output", "Formatter, installer and task output shows here.").EmptyIcon(icons.ScrollText)
	}
	if a.outputEd == nil || a.outputDirty {
		var scroll float32
		if a.outputEd != nil {
			scroll = a.outputEd.ScrollPx
			a.outputEd.Close()
		}
		a.outputEd = ui.NewEditor([]byte(strings.TrimRight(a.output.String(), "\n")), highlight.Noop{},
			ui.WithReadOnly(), ui.WithoutGutter(), ui.WithSoftWrap(true))
		a.outputEd.ScrollPx = scroll
		a.outputDirty = false
	}
	return ui.ViewOf(a.outputEd).Grow(1)
}
