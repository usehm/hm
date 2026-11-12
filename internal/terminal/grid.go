// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// pos is a cell in the whole buffer: row 0 is the oldest scrollback line,
// and the screen starts at row grid.sb.
type pos struct{ row, col int }

func (p pos) before(q pos) bool { return p.row < q.row || p.row == q.row && p.col < q.col }

// grid reads the scrollback and screen as one list of rows. Hold mu.
type grid struct {
	emu        *vt.Emulator
	sb         int // scrollback rows; none on the alternate screen
	rows, cols int
}

func (s *Session) grid() grid {
	g := grid{emu: s.emu, rows: s.rows, cols: s.cols}
	if !s.emu.IsAltScreen() {
		g.sb = s.emu.ScrollbackLen()
	}
	return g
}

func (g grid) total() int { return g.sb + g.rows }

// cell returns the cell at row, col, or nil past the end of a stored line.
func (g grid) cell(row, col int) *uv.Cell {
	if row < g.sb {
		return g.emu.ScrollbackCellAt(col, row)
	}
	return g.emu.CellAt(col, row-g.sb)
}

// text returns the cells from a up to (not including) b, one line per row
// with trailing blanks trimmed.
func (g grid) text(a, b pos) string {
	if b.before(a) {
		a, b = b, a
	}
	var out strings.Builder
	for row := max(a.row, 0); row <= b.row && row < g.total(); row++ {
		from, to := 0, g.cols
		if row == a.row {
			from = a.col
		}
		if row == b.row {
			to = min(b.col, g.cols)
		}
		var line strings.Builder
		for col := from; col < to; col++ {
			c := g.cell(row, col)
			switch {
			case c == nil:
				line.WriteByte(' ')
			case c.Width == 0 && c.Content == "":
				// The right half of a wide character.
			case c.Content == "":
				line.WriteByte(' ')
			default:
				line.WriteString(c.Content)
			}
		}
		out.WriteString(strings.TrimRight(line.String(), " "))
		if row < b.row {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// wordAt returns the word around p, for double-click selection.
func (g grid) wordAt(p pos) (pos, pos) {
	isWord := func(col int) bool {
		c := g.cell(p.row, col)
		if c == nil || c.Content == "" || c.Content == " " {
			return false
		}
		return !strings.ContainsAny(c.Content, " \t\"'`()[]{}<>|;,")
	}
	if !isWord(p.col) {
		return p, pos{p.row, p.col + 1}
	}
	a, b := p.col, p.col
	for a > 0 && isWord(a-1) {
		a--
	}
	for b < g.cols-1 && isWord(b+1) {
		b++
	}
	return pos{p.row, a}, pos{p.row, b + 1}
}
