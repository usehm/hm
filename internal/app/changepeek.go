// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/diff"
)

// changePeek is the popup a change bar opens: what the lines were in HEAD
// and what they are now, with a way to put them back.
type changePeek struct {
	path   string
	rev    uint64 // buffer revision the hunk was computed for
	a, b   []string
	hunks  []diff.Hunk
	cur    int // the hunk shown
	anchor int // the line the popup sits under
}

// changeHunks diffs ed against its HEAD version; false when there is no
// tracked base yet.
func (a *App) changeHunks(ed *ui.Editor) (base, cur []string, hunks []diff.Hunk, ok bool) {
	b := a.gitst.bases[ed.Path]
	if b == nil || !b.loaded || !b.tracked {
		return nil, nil, nil, false
	}
	base, cur = diff.Lines(string(b.data)), diff.Lines(string(ed.Bytes()))
	return base, cur, diff.Hunks(base, cur, 0), true
}

// hunkAt is the hunk whose change bar is on line; a deletion's bar sits on
// the line after it.
func hunkAt(hunks []diff.Hunk, line int) int {
	for i, h := range hunks {
		if line >= h.B0 && (line < h.B1 || line == h.B1 && h.B0 == h.B1) {
			return i
		}
	}
	for i, h := range hunks {
		if line == h.B1 { // a modification that removed more lines than it added
			return i
		}
	}
	return -1
}

// showChange opens the change peek for the hunk with a bar on line.
func (a *App) showChange(ed *ui.Editor, line int) {
	base, cur, hunks, ok := a.changeHunks(ed)
	if !ok {
		return
	}
	i := hunkAt(hunks, line)
	if i < 0 {
		return
	}
	a.peek = &changePeek{path: ed.Path, rev: ed.Revision(), a: base, b: cur, hunks: hunks, cur: i}
	a.peek.anchor = peekAnchor(hunks[i])
}

// peekAnchor is the last line of h in the buffer, or the line after a
// deletion.
func peekAnchor(h diff.Hunk) int {
	if h.B1 > h.B0 {
		return h.B1 - 1
	}
	return max(h.B0-1, 0)
}

// stepChange moves the peek, or the caret when no peek is open, to the next
// (dir 1) or previous (dir -1) change, wrapping around.
func (a *App) stepChange(ed *ui.Editor, dir int) {
	if ed == nil {
		return
	}
	_, _, hunks, ok := a.changeHunks(ed)
	if !ok || len(hunks) == 0 {
		return
	}
	line, _ := ed.LineCol(ed.Caret())
	i := -1
	if p := a.peek; p != nil && p.path == ed.Path && p.rev == ed.Revision() {
		i = (p.cur + dir + len(hunks)) % len(hunks)
	} else if dir > 0 {
		i = 0
		for j, h := range hunks {
			if h.B0 > line {
				i = j
				break
			}
		}
	} else {
		i = len(hunks) - 1
		for j := len(hunks) - 1; j >= 0; j-- {
			if hunks[j].B0 < line {
				i = j
				break
			}
		}
	}
	off := ed.Offset(hunks[i].B0, 0)
	ed.SetSelection(off, off)
	if a.peek != nil {
		a.showChange(ed, hunks[i].B0)
	}
}

// revertChange puts the peek's hunk back to its HEAD lines, as one undo
// step.
func (a *App) revertChange(ed *ui.Editor) {
	p := a.peek
	if p == nil || ed == nil || ed.Path != p.path || ed.Revision() != p.rev {
		return
	}
	h := p.hunks[p.cur]
	text := ed.Bytes()
	lo, hi := lineStart(text, h.B0), lineStart(text, h.B1)
	old := strings.Join(p.a[h.A0:h.A1], "\n")
	if h.A1 > h.A0 && (hi < len(text) || len(text) > 0 && text[len(text)-1] == '\n') {
		old += "\n"
	}
	ed.Replace(lo, hi, old)
	a.peek = nil
}

// lineStart is the byte offset where 0-based line starts, or len(text) past
// the last line.
func lineStart(text []byte, line int) int {
	off := 0
	for i := 0; i < line; i++ {
		j := strings.IndexByte(string(text[off:]), '\n')
		if j < 0 {
			return len(text)
		}
		off += j + 1
	}
	return off
}

// changePeekView is the open peek over ed, or nil.
func (a *App) changePeekView(c *ui.Ctx, ed *ui.Editor) ui.View {
	p := a.peek
	if p == nil || ed == nil || p.path != ed.Path {
		return nil
	}
	if ed.Revision() != p.rev {
		a.peek = nil // the text moved on; the hunk no longer lines up
		return nil
	}
	anchor, ok := ed.LineRect(p.anchor)
	if !ok {
		return nil
	}
	th := c.Theme()
	h := p.hunks[p.cur]
	removed, added := h.A1-h.A0, h.B1-h.B0
	summary := fmt.Sprintf("Change %d of %d · %s removed, %s added", p.cur+1, len(p.hunks),
		plural(removed, "line"), plural(added, "line"))
	header := ui.Row(
		ui.Caption(summary).Grow(1).Shrink(1),
		ui.IconButton("peek-prev", icons.ArrowUp).Tooltip("Previous change ("+a.key("editor.prevChange")+")").
			OnClick(func() { a.stepChange(ed, -1) }),
		ui.IconButton("peek-next", icons.ArrowDown).Tooltip("Next change ("+a.key("editor.nextChange")+")").
			OnClick(func() { a.stepChange(ed, 1) }),
		ui.Button("peek-revert", ui.Text("Revert")).IconStart(icons.Undo2).Subtle().
			OnClick(func() { a.revertChange(ed) }),
		ui.IconButton("peek-close", icons.X).Tooltip("Close").OnClick(func() { a.peek = nil }),
	).Gap(th.Spacing.XS).Align(ui.AlignCenter)
	lines := min(removed+added, maxHunkLines+1)
	height := float32(lines)*20 + 60
	width := min(anchor.W-60, 720)
	body := ui.Column(header, ui.Scroll("peek-lines", diffLines(c, p.a, p.b, h)).Grow(1)).
		Gap(th.Spacing.XS).Grow(1)
	anchor.X += 48 // past the gutter, under the code
	return ui.PopoverAt("change-peek", anchor, body).Open(true).
		OnOpenChange(func(v bool) {
			if !v {
				a.peek = nil
			}
		}).Width(width).Height(min(height, 360))
}
