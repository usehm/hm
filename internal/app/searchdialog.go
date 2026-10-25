package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/search"
	"github.com/mirzakhany/hm/internal/workspace"
)

// searchDebounce waits for typing to pause before searching.
const searchDebounce = 150 * time.Millisecond

// projectSearch is the search dialog's state.
type projectSearch struct {
	open        bool
	q           search.Query
	replace     string
	showReplace bool

	results   []search.FileResult
	total     int
	truncated bool
	running   bool
	err       string
	gen       int
	cancel    context.CancelFunc
	msgs      chan searchMsg
	pending   time.Time // run once this passes; zero when nothing is queued
	collapsed map[string]bool
	confirm   bool // replace-all confirmation is showing

	selFile, selMatch int
	preview           string // path in the right pane
	previewRev        uint64
	// borrowed holds buffers opened only for the preview (no tab).
	borrowed map[string]bool
}

type searchMsg struct {
	gen  int
	fr   *search.FileResult
	done bool
	err  error
}

// openSearch shows the project search dialog, seeded with the selection.
func (a *App) openSearch() {
	s := &a.search
	if s.collapsed == nil {
		s.collapsed, s.borrowed = map[string]bool{}, map[string]bool{}
		s.msgs = make(chan searchMsg, 256)
	}
	if ed := a.ws.ActiveEditor(); ed != nil && ed.HasSelection() {
		if sel := ed.SelectedText(); len(sel) < 200 && !containsNewline(sel) {
			s.q.Pattern = sel
		}
	}
	s.open, s.confirm = true, false
	if s.q.Pattern != "" {
		a.runSearch()
	}
	w, h := a.ctx.Viewport()
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title:     "Search",
		Width:     max(720, w*0.9),
		Height:    max(480, h*0.86),
		Body:      a.searchBody,
		OnDismiss: a.closeSearch,
		Actions:   []ui.DialogAction{{Label: "Close", OnClick: a.closeSearch}},
	})
}

func containsNewline(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return true
		}
	}
	return false
}

// closeSearch stops the search and gives preview-only buffers a tab when
// they were edited, or releases them.
func (a *App) closeSearch() {
	s := &a.search
	s.open, s.confirm = false, false
	if s.cancel != nil {
		s.cancel()
	}
	for p := range s.borrowed {
		if ed := a.bufs.Get(p); ed != nil && ed.Modified() {
			_, _ = a.ws.Open(p)
		} else if !a.hasTab(p) {
			a.bufs.Close(p)
		}
		delete(s.borrowed, p)
	}
	s.preview = ""
}

func (a *App) hasTab(path string) bool {
	for _, t := range a.ws.Tabs {
		if t.Kind == workspace.KindFile && t.Path == path {
			return true
		}
	}
	return false
}

// queueSearch runs the search once typing pauses.
func (a *App) queueSearch() { a.search.pending = time.Now().Add(searchDebounce) }

// runSearch starts a search, cancelling the one in flight. Open buffers are
// snapshotted here, on the UI goroutine, since editors are not thread-safe.
func (a *App) runSearch() {
	s := &a.search
	s.pending = time.Time{}
	if s.cancel != nil {
		s.cancel()
	}
	s.gen++
	s.results, s.total, s.truncated, s.err = nil, 0, false, ""
	s.selFile, s.selMatch = -1, 0
	if s.q.Pattern == "" {
		s.running = false
		return
	}
	if _, err := s.q.Compile(); err != nil {
		s.running, s.err = false, err.Error()
		return
	}
	overlay := map[string][]byte{}
	var dirty []string
	for _, p := range a.openBuffers() {
		if ed := a.bufs.Get(p); ed != nil {
			overlay[p] = append([]byte(nil), ed.Bytes()...)
			if ed.Modified() {
				dirty = append(dirty, p)
			}
		}
	}
	cfg := a.cfg.Current().Search
	opts := search.Options{
		Engine: cfg.Engine, Exclude: cfg.Exclude, MaxResults: cfg.MaxResults, Dirty: dirty,
		Overlay: func(p string) ([]byte, bool) { b, ok := overlay[p]; return b, ok },
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.running = cancel, true
	gen, q, root, msgs := s.gen, s.q, a.ws.Root, s.msgs
	go func() {
		err := search.Run(ctx, root, q, opts, func(fr search.FileResult) bool {
			select {
			case msgs <- searchMsg{gen: gen, fr: &fr}:
			case <-ctx.Done():
				return false
			}
			a.invalidate()
			return true
		})
		select {
		case msgs <- searchMsg{gen: gen, done: true, err: err}:
		case <-ctx.Done():
		}
		a.invalidate()
	}()
}

// openBuffers lists the paths with an open buffer: tabs and the preview.
func (a *App) openBuffers() []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range a.ws.Tabs {
		if t.Kind == workspace.KindFile && !seen[t.Path] {
			seen[t.Path] = true
			out = append(out, t.Path)
		}
	}
	for p := range a.search.borrowed {
		if !seen[p] {
			out = append(out, p)
		}
	}
	return out
}

// pumpSearch takes results in and runs a queued search; call from Body.
func (a *App) pumpSearch(c *ui.Ctx) {
	s := &a.search
	if !s.open {
		return
	}
	for {
		select {
		case m := <-s.msgs:
			if m.gen != s.gen {
				continue
			}
			if m.done {
				s.running = false
				if s.selFile < 0 && len(s.results) > 0 {
					a.selectMatch(0, 0)
				}
				switch {
				case errors.Is(m.err, search.ErrTruncated):
					s.truncated = true
				case m.err != nil && !errors.Is(m.err, context.Canceled):
					s.err = m.err.Error()
				}
				continue
			}
			var at int
			s.results, at = insertSorted(s.results, *m.fr)
			s.total += len(m.fr.Matches)
			if s.selFile >= 0 && at <= s.selFile {
				s.selFile++ // keep the selected row selected
			}
		default:
			goto drained
		}
	}
drained:
	// Edits in the preview move matches: search again once they pause.
	if ed := a.bufs.Get(s.preview); ed != nil && ed.Revision() != s.previewRev {
		s.previewRev = ed.Revision()
		a.queueSearch()
	}
	if !s.pending.IsZero() {
		if wait := time.Until(s.pending); wait > 0 {
			c.Animate(wait)
		} else {
			a.runSearch()
		}
	}
	if s.running {
		c.Animate(100 * time.Millisecond)
	}
}

// insertSorted adds fr in path order and returns where it went.
func insertSorted(rs []search.FileResult, fr search.FileResult) ([]search.FileResult, int) {
	i := len(rs)
	for i > 0 && rs[i-1].Path > fr.Path {
		i--
	}
	rs = append(rs, search.FileResult{})
	copy(rs[i+1:], rs[i:])
	rs[i] = fr
	return rs, i
}

// selectMatch shows match m of file f in the preview, selected.
func (a *App) selectMatch(f, m int) {
	s := &a.search
	if f < 0 || f >= len(s.results) || m < 0 || m >= len(s.results[f].Matches) {
		return
	}
	s.selFile, s.selMatch = f, m
	fr := s.results[f]
	ed := a.bufs.Get(fr.Path)
	if ed == nil {
		var err error
		if ed, err = a.bufs.Open(fr.Path); err != nil {
			s.err = err.Error()
			return
		}
		s.borrowed[fr.Path] = true
	}
	s.preview, s.previewRev = fr.Path, ed.Revision()
	mt := fr.Matches[m]
	ed.SetSelection(mt.Start, mt.End)
}

// step moves the selection to the next (dir 1) or previous (-1) match.
func (a *App) step(dir int) {
	s := &a.search
	if len(s.results) == 0 {
		return
	}
	if s.selFile < 0 {
		a.selectMatch(0, 0)
		return
	}
	f, m := s.selFile, s.selMatch+dir
	for m < 0 || m >= len(s.results[f].Matches) {
		f = (f + dir + len(s.results)) % len(s.results)
		if dir > 0 {
			m = 0
		} else {
			m = len(s.results[f].Matches) - 1
		}
	}
	a.selectMatch(f, m)
}

func (a *App) searchRegexp() *regexp.Regexp {
	re, _ := a.search.q.Compile()
	return re
}

// bufferFor returns the buffer for path, borrowing one for files not open.
func (a *App) bufferFor(path string) (*ui.Editor, error) {
	if ed := a.bufs.Get(path); ed != nil {
		return ed, nil
	}
	ed, err := a.bufs.Open(path)
	if err == nil {
		a.search.borrowed[path] = true
	}
	return ed, err
}

// replaceOne replaces match m of file f.
func (a *App) replaceOne(f, m int) {
	s := &a.search
	re := a.searchRegexp()
	if re == nil || f >= len(s.results) || m >= len(s.results[f].Matches) {
		return
	}
	fr := s.results[f]
	ed, err := a.bufferFor(fr.Path)
	if err != nil {
		a.fail("Replace failed", err)
		return
	}
	out, n := search.ReplaceMatches(re, s.q.Regex, ed.Bytes(), fr.Matches[m:m+1], s.replace)
	if n == 0 {
		a.toast("That match changed; searching again", ui.ToastInfo)
	} else {
		ed.ApplyText(string(out))
	}
	a.runSearch()
}

// replaceFile replaces every match in file f as one undoable edit.
func (a *App) replaceFile(f int) int {
	s := &a.search
	re := a.searchRegexp()
	if re == nil || f >= len(s.results) {
		return 0
	}
	path := s.results[f].Path
	ed, err := a.bufferFor(path)
	if err != nil {
		a.fail("Replace failed", err)
		return 0
	}
	out, n := search.ReplaceAll(re, s.q.Regex, ed.Bytes(), s.replace)
	if n > 0 {
		ed.ApplyText(string(out))
		if !a.hasTab(path) {
			// Changed files get a tab, so they can be reviewed and saved.
			_, _ = a.ws.Open(path)
			delete(s.borrowed, path)
		}
	}
	return n
}

// replaceAll replaces in every file with results, one undoable edit per file.
func (a *App) replaceAll() {
	s := &a.search
	n, files := 0, 0
	for f := range s.results {
		if k := a.replaceFile(f); k > 0 {
			n += k
			files++
		}
	}
	s.confirm = false
	a.toast(fmt.Sprintf("Replaced %d %s in %d %s", n, matchWord(n), files, plural(files, "file")), ui.ToastSuccess)
	a.runSearch()
}

func (a *App) searchBody(c *ui.Ctx) ui.View {
	return ui.Splitter("search-split", ui.Horizontal, a.searchLeft(c), a.searchPreview(c)).
		Percents(42, 58).MinSizes(320, 320).Grow(1)
}

func (a *App) searchLeft(c *ui.Ctx) ui.View {
	th := c.Theme()
	s := &a.search
	toggle := func(id string, icon icons.Icon, tip string, on bool, set func(bool)) ui.View {
		b := ui.IconButton(id, icon).Tooltip(tip).OnClick(func() { set(!on); a.runSearch() })
		if on {
			b = b.Background(ui.TokenListActive).Radius(th.Radius.Small)
		}
		return b
	}
	replaceIcon := icons.ChevronRight
	if s.showReplace {
		replaceIcon = icons.ChevronDown
	}
	rows := []ui.View{
		ui.Row(
			ui.IconButton("search-toggle-replace", replaceIcon).Tooltip("Replace").
				OnClick(func() { s.showReplace = !s.showReplace }),
			ui.TextField("search-query", s.q.Pattern).Placeholder("Search").IconStart(icons.Search).
				OnChange(func(v string) { s.q.Pattern = v; a.queueSearch() }).
				OnSubmit(func(string) { a.step(1) }).
				DefaultFocus().Grow(1),
			toggle("search-case", icons.CaseSensitive, "Match case", s.q.Case, func(v bool) { s.q.Case = v }),
			toggle("search-word", icons.WholeWord, "Whole word", s.q.Word, func(v bool) { s.q.Word = v }),
			toggle("search-regex", icons.Regex, "Regular expression", s.q.Regex, func(v bool) { s.q.Regex = v }),
		).Gap(th.Spacing.XS),
	}
	if s.showReplace {
		hint := "Replace"
		if s.q.Regex {
			hint = "Replace ($1 for groups)"
		}
		rows = append(rows, ui.Row(
			ui.TextField("search-replace", s.replace).Placeholder(hint).IconStart(icons.Replace).
				OnChange(func(v string) { s.replace = v }).Grow(1),
			ui.IconButton("search-replace-all", icons.ReplaceAll).Tooltip("Replace all").
				Disabled(s.total == 0).OnClick(func() { s.confirm = true }),
		).Gap(th.Spacing.XS).PaddingLeft(30))
	}
	rows = append(rows,
		ui.Row(
			ui.TextField("search-include", s.q.Include).Placeholder("Files to include (*.go, cmd/**)").
				OnChange(func(v string) { s.q.Include = v; a.queueSearch() }).Grow(1),
			ui.TextField("search-exclude", s.q.Exclude).Placeholder("Files to exclude").
				OnChange(func(v string) { s.q.Exclude = v; a.queueSearch() }).Grow(1),
		).Gap(th.Spacing.XS).PaddingLeft(30),
	)
	if s.confirm {
		rows = append(rows, ui.Row(
			ui.Text(fmt.Sprintf("Replace %d %s in %d %s?", s.total, matchWord(s.total),
				len(s.results), plural(len(s.results), "file"))).Grow(1),
			ui.Button("search-confirm-no", ui.Text("Cancel")).OnClick(func() { s.confirm = false }),
			ui.Button("search-confirm-yes", ui.Text("Replace")).Primary().OnClick(a.replaceAll),
		).Gap(th.Spacing.S).Padding(th.Spacing.S).Radius(th.Radius.Medium).Background(ui.TokenChromeMuted))
	}
	rows = append(rows, a.searchStatus(c), a.searchResults(c))
	return ui.Column(rows...).Gap(th.Spacing.S).PaddingRight(th.Spacing.S).Grow(1)
}

func (a *App) searchStatus(c *ui.Ctx) ui.View {
	s := &a.search
	var parts []ui.View
	switch {
	case s.err != "":
		parts = append(parts, ui.Caption(s.err).Style(ui.Spec{}.TextColor(ui.TokenError)))
	case s.q.Pattern == "":
		parts = append(parts, ui.Caption("Type to search the workspace"))
	default:
		msg := fmt.Sprintf("%d %s in %d %s", s.total, plural(s.total, "result"), len(s.results), plural(len(s.results), "file"))
		if s.truncated {
			msg += " (stopped at the result limit)"
		}
		parts = append(parts, ui.Caption(msg))
	}
	if s.running {
		parts = append(parts, ui.Spinner("search-spin", 12))
	}
	return ui.Row(parts...).Gap(c.Theme().Spacing.XS)
}

func (a *App) searchResults(c *ui.Ctx) ui.View {
	th := c.Theme()
	s := &a.search
	rows := []ui.View{}
	for fi, fr := range s.results {
		fi, fr := fi, fr
		chev := icons.ChevronDown
		if s.collapsed[fr.Path] {
			chev = icons.ChevronRight
		}
		fileRow := ui.Row(
			ui.Button(fmt.Sprintf("sr-file-%d", fi), ui.Row(
				ui.Icon(chev, 14, th.ForegroundMuted),
				ui.Strong(filepath.Base(fr.Path)),
				ui.Caption(a.ws.Rel(filepath.Dir(fr.Path))).Ellipsis(ui.EllipsisMiddle).Grow(1),
				ui.Badge(fmt.Sprint(len(fr.Matches))).Tone(ui.BadgeMuted),
			).Gap(th.Spacing.XS).Grow(1)).Ghost().HoverFill().Grow(1).
				OnClick(func() { s.collapsed[fr.Path] = !s.collapsed[fr.Path] }),
		)
		if s.showReplace {
			fileRow = ui.Row(fileRow.Grow(1),
				ui.IconButton(fmt.Sprintf("sr-file-replace-%d", fi), icons.ReplaceAll).Tooltip("Replace all in file").
					OnClick(func() { a.replaceFile(fi); a.runSearch() }))
		}
		rows = append(rows, fileRow)
		if s.collapsed[fr.Path] {
			continue
		}
		for mi, m := range fr.Matches {
			mi, m := mi, m
			before, hit, after := splitLine(m)
			row := ui.Button(fmt.Sprintf("sr-%d-%d", fi, mi), ui.Row(
				ui.Caption(fmt.Sprint(m.Line+1)).Width(36),
				ui.Text(before).Shrink(0),
				ui.Text(hit).Background(ui.TokenSelection).Shrink(0),
				ui.Text(after).Ellipsis(ui.EllipsisEnd).Grow(1),
			).Grow(1)).Ghost().HoverFill().Grow(1).OnClick(func() { a.selectMatch(fi, mi) })
			if fi == s.selFile && mi == s.selMatch {
				row = row.Background(ui.TokenListActive)
			}
			line := ui.Row(row).PaddingLeft(th.Spacing.L)
			if s.showReplace {
				line = ui.Row(row.Grow(1), ui.IconButton(fmt.Sprintf("sr-replace-%d-%d", fi, mi), icons.Replace).
					Tooltip("Replace").OnClick(func() { a.replaceOne(fi, mi) })).PaddingLeft(th.Spacing.L)
			}
			rows = append(rows, line)
		}
	}
	if len(rows) == 0 {
		if s.q.Pattern != "" && !s.running && s.err == "" {
			return ui.ViewOf(ui.EmptyState("No results", "Try another search or fewer filters").EmptyIcon(icons.SearchX)).Grow(1)
		}
		return ui.Spacer()
	}
	return ui.Scroll("search-results", ui.Column(rows...).Gap(th.Spacing.XXS)).Grow(1)
}

// maxBefore is how much of a line before a match a result row shows.
const maxBefore = 32

// splitLine cuts a match's line around the match, trimming leading space.
func splitLine(m search.Match) (string, string, string) {
	t := m.LineText
	lo, hi := max(0, min(m.ColInLine, len(t))), max(0, min(m.EndInLine, len(t)))
	if hi < lo {
		hi = lo
	}
	start := 0
	for start < lo && (t[start] == ' ' || t[start] == '\t') {
		start++
	}
	before := t[start:lo]
	// Keep the text just before the match; the row ellipsizes the end.
	if r := []rune(before); len(r) > maxBefore {
		before = "…" + string(r[len(r)-maxBefore:])
	}
	return before, t[lo:hi], t[hi:]
}

func (a *App) searchPreview(c *ui.Ctx) ui.View {
	th := c.Theme()
	s := &a.search
	ed := a.bufs.Get(s.preview)
	if ed == nil {
		return ui.ViewOf(ui.EmptyState("No file selected", "Pick a result to see and edit it here").EmptyIcon(icons.FileSearch)).Grow(1)
	}
	title := a.ws.Rel(s.preview)
	if ed.Modified() {
		title += " ●"
	}
	return ui.Column(
		ui.Row(
			ui.Caption(title).Ellipsis(ui.EllipsisMiddle).Grow(1),
			ui.Button("search-open-tab", ui.Text("Open in tab")).Subtle().IconStart(icons.SquareArrowOutUpRight).
				OnClick(func() {
					path := s.preview
					a.ctx.Dialogs().Close()
					a.closeSearch()
					a.openFile(path)
				}),
		).Gap(th.Spacing.S).PaddingXY(th.Spacing.S, th.Spacing.XXS),
		ui.ViewOf(ed).Grow(1),
	).Grow(1).Border(ui.TokenBorder, th.Stroke.Thin).Radius(th.Radius.Medium)
}

func matchWord(n int) string {
	if n == 1 {
		return "match"
	}
	return "matches"
}
