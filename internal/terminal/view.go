// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"strings"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/layout"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/shape"
	"github.com/mirzakhany/yoga/ui"
)

const (
	pad        = 6 // inner margin around the grid
	blinkHalf  = 530 * time.Millisecond
	multiClick = 400 * time.Millisecond
)

// View draws a session and sends it keys, text and mouse events. Build it
// once per session and place it with ui.ViewOf.
type View struct {
	s *Session

	// FontSize is the text size in logical pixels; 0 uses the editor's.
	FontSize float32
	// Pass lets an app keep chords it needs (such as the one that hides the
	// terminal) even though the terminal would claim them.
	Pass func(input.KeyEvent) bool

	host    *layout.Element
	focused bool
	pal     Palette
	cellW   float32
	cellH   float32
	clip    input.Clipboard
	blink   time.Time

	scroll   int // rows scrolled back into history
	lastSB   int
	wheelAcc float32

	sel struct {
		a, b   pos
		on     bool
		drag   bool
		unit   int // 1 = cells, 2 = words, 3 = lines
		anchor [2]pos
	}
	lastClick  time.Time
	lastClickP pos
	clicks     int
	reporting  bool // a press went to the program, so its release must too
}

// NewView returns the widget for s.
func NewView(s *Session) *View {
	v := &View{s: s, blink: time.Now()}
	v.host = layout.New(layout.Box().FlexGrow(1))
	v.host.Clip = true
	v.host.Paint = v.paint
	v.host.OnMouse = v.onMouse
	return v
}

// Session returns the session the view shows.
func (v *View) Session() *Session { return v.s }

var (
	_ ui.Focusable  = (*View)(nil)
	_ ui.KeyClaimer = (*View)(nil)
)

func (v *View) Focus()                   { v.focused = true; v.blink = time.Now() }
func (v *View) Blur()                    { v.focused = false }
func (v *View) Focused() bool            { return v.focused }
func (v *View) CapturesTab() bool        { return true }
func (v *View) FocusOnClick() bool       { return true }
func (v *View) FocusEl() *layout.Element { return v.host }

// ClaimsKey takes keys ahead of app shortcuts; see claims.
func (v *View) ClaimsKey(ev input.KeyEvent) bool {
	if v.Pass != nil && v.Pass(ev) {
		return false
	}
	return claims(ev)
}

// Layout sizes the session to the space the view got last frame.
func (v *View) Layout(c *ui.Ctx) *layout.Element {
	c.Focus().Add(v)
	v.clip = c.Clipboard()
	v.pal = PaletteFor(c.Theme())
	eng := c.Text()
	v.cellW, v.cellH = eng.MeasureMonoAt("M", v.fontSize(eng))
	v.cellH = float32(int(v.cellH + 0.5))
	if f := v.host.Frame; f.W > 0 && f.H > 0 && v.cellW > 0 {
		cols, rows := v.gridSize(f)
		if c0, r0 := v.s.Size(); cols != c0 || rows != r0 {
			v.s.Resize(cols, rows)
			c.Invalidate()
		}
	}
	if v.focused {
		since := time.Since(v.blink) % blinkHalf
		c.Animate(blinkHalf - since)
	}
	return v.host
}

func (v *View) gridSize(f render.Rect) (cols, rows int) {
	return int((f.W - 2*pad) / v.cellW), int((f.H - 2*pad) / v.cellH)
}

// fontSize is the size paint shapes at; Layout settles cellW from it.
func (v *View) fontSize(eng *shape.Engine) render.Px {
	if v.FontSize > 0 {
		return render.Px(v.FontSize)
	}
	return eng.MetricsMono().LineHeight / 1.4
}

// --- keyboard ---------------------------------------------------------------

// HandleText types what the keyboard produced.
func (v *View) HandleText(runes []rune) {
	if len(runes) == 0 {
		return
	}
	v.toBottom()
	v.s.SendText(string(runes))
}

// HandleKeys sends keys to the program; Copy and Paste chords (Cmd+C/V on
// macOS, Ctrl+Shift+C/V elsewhere) act on the clipboard instead.
func (v *View) HandleKeys(keys []input.KeyEvent) {
	for _, ev := range keys {
		clipChord := ev.Mods.Has(input.ModSuper) || ev.Mods.Has(input.ModCtrl|input.ModShift)
		if clipChord {
			switch ev.Key {
			case input.KeyC:
				v.Copy()
				continue
			case input.KeyV:
				v.Paste()
				continue
			case input.KeyA:
				v.SelectAll()
				continue
			}
		}
		if ev.Mods.Has(input.ModSuper) {
			continue
		}
		if v.s.sendKeyEvent(ev) {
			v.toBottom()
		}
	}
}

func (v *View) toBottom() {
	v.scroll = 0
	v.sel.on = false
	v.blink = time.Now()
}

// Copy puts the selection on the clipboard.
func (v *View) Copy() {
	if t := v.Selection(); t != "" && v.clip != nil {
		v.clip.Set(t)
	}
}

// Paste sends the clipboard to the program.
func (v *View) Paste() {
	if v.clip == nil {
		return
	}
	if t := v.clip.Get(); t != "" {
		v.toBottom()
		v.s.Paste(t)
	}
}

// Selection returns the selected text.
func (v *View) Selection() string {
	if !v.sel.on {
		return ""
	}
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	return v.s.grid().text(v.sel.a, v.sel.b)
}

// HasSelection reports whether some text is selected.
func (v *View) HasSelection() bool { return v.sel.on && v.sel.a != v.sel.b }

// SelectAll selects the scrollback and the screen.
func (v *View) SelectAll() {
	v.s.mu.Lock()
	g := v.s.grid()
	v.s.mu.Unlock()
	v.sel.a, v.sel.b, v.sel.on = pos{}, pos{row: g.total() - 1, col: g.cols}, true
}

// Clear drops the scrollback and asks the shell to redraw (Ctrl+L).
func (v *View) Clear() {
	v.s.ClearScrollback()
	v.toBottom()
	v.s.SendKey(vt.KeyPressEvent{Code: 'l', Mod: vt.ModCtrl})
}

// --- mouse ------------------------------------------------------------------

// cellAt maps a point to a buffer position; out-of-grid points clamp.
func (v *View) cellAt(f render.Rect, x, y float32, g grid) pos {
	col := int((x - f.X - pad) / v.cellW)
	row := int((y - f.Y - pad) / v.cellH)
	if y < f.Y+pad {
		row = -1
	}
	col = min(max(col, 0), g.cols)
	row = min(max(row, -1), g.rows)
	if row < 0 {
		col = 0
		row = 0
	} else if row >= g.rows {
		row, col = g.rows-1, g.cols
	}
	return pos{row: g.sb - v.scroll + row, col: col}
}

func (v *View) onMouse(el *layout.Element, m *input.Mouse) {
	f := el.Frame
	inside := f.Contains(m.X, m.Y)
	if v.cellW <= 0 {
		return
	}
	if inside {
		m.SetCursor(input.CursorText)
	}
	v.s.mu.Lock()
	g := v.s.grid()
	tracking := !v.s.exited && v.s.mouseTracking()
	v.s.mu.Unlock()
	scr := func() (int, int) {
		p := v.cellAt(f, m.X, m.Y, g)
		return p.col, p.row - (g.sb - v.scroll)
	}

	if inside && m.ScrollY != 0 {
		v.wheel(m, g, tracking, scr)
		m.ScrollY = 0
		m.Consumed = true
	}

	report := tracking && !m.Mods.Has(input.ModShift)
	switch {
	case m.Pressed && inside && report:
		x, y := scr()
		v.s.SendMouse(vt.MouseClick{X: x, Y: y, Button: vt.MouseLeft, Mod: mouseMods(m.Mods)})
		v.reporting = true
		m.Consumed = true
	case v.reporting && m.Released:
		x, y := scr()
		v.s.SendMouse(vt.MouseRelease{X: x, Y: y, Button: vt.MouseLeft, Mod: mouseMods(m.Mods)})
		v.reporting = false
	case v.reporting && m.Down:
		x, y := scr()
		v.s.SendMouse(vt.MouseMotion{X: x, Y: y, Button: vt.MouseLeft, Mod: mouseMods(m.Mods)})
	case m.Pressed && inside:
		v.press(v.cellAt(f, m.X, m.Y, g), g, m.Mods.Has(input.ModShift))
		m.Consumed = true
	case v.sel.drag && m.Down:
		v.extend(v.cellAt(f, m.X, m.Y, g), g)
	case v.sel.drag && m.Released:
		v.sel.drag = false
		if v.sel.a == v.sel.b {
			v.sel.on = false
		}
	}
	if inside && m.RightPressed && report {
		x, y := scr()
		v.s.SendMouse(vt.MouseClick{X: x, Y: y, Button: vt.MouseRight, Mod: mouseMods(m.Mods)})
		m.Consumed = true
	}
}

func (v *View) wheel(m *input.Mouse, g grid, tracking bool, scr func() (int, int)) {
	v.wheelAcc += m.ScrollY * ui.WheelPixelsPerUnit
	lines := int(v.wheelAcc / v.cellH)
	if lines == 0 {
		return
	}
	v.wheelAcc -= float32(lines) * v.cellH
	switch {
	case tracking && !m.Mods.Has(input.ModShift):
		x, y := scr()
		btn := vt.MouseWheelUp
		if lines < 0 {
			btn = vt.MouseWheelDown
		}
		for range min(abs(lines), 5) {
			v.s.SendMouse(vt.MouseWheel{X: x, Y: y, Button: btn})
		}
	case g.sb == 0 && v.s.isAltScreen():
		// Full-screen programs without mouse support (less, man) scroll
		// with the arrow keys, as in other terminals.
		code := vt.KeyUp
		if lines < 0 {
			code = vt.KeyDown
		}
		for range min(abs(lines), 5) {
			v.s.SendKey(vt.KeyPressEvent{Code: code})
		}
	default:
		v.scroll = min(max(v.scroll+lines, 0), g.sb)
	}
}

func (s *Session) isAltScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.IsAltScreen()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func mouseMods(m input.Mod) vt.KeyMod {
	var k vt.KeyMod
	if m.Has(input.ModShift) {
		k |= vt.ModShift
	}
	if m.Has(input.ModAlt) {
		k |= vt.ModAlt
	}
	if m.Has(input.ModCtrl) {
		k |= vt.ModCtrl
	}
	return k
}

func (v *View) press(p pos, g grid, extend bool) {
	now := time.Now()
	if now.Sub(v.lastClick) < multiClick && p.row == v.lastClickP.row && abs(p.col-v.lastClickP.col) <= 1 {
		v.clicks = v.clicks%3 + 1
	} else {
		v.clicks = 1
	}
	v.lastClick, v.lastClickP = now, p
	if extend && v.sel.on {
		v.sel.drag = true
		v.extend(p, g)
		return
	}
	v.sel.unit = v.clicks
	a, b := v.unitAt(p, g)
	v.sel.anchor = [2]pos{a, b}
	v.sel.a, v.sel.b, v.sel.on, v.sel.drag = a, b, true, true
}

func (v *View) unitAt(p pos, g grid) (pos, pos) {
	switch v.sel.unit {
	case 2:
		return g.wordAt(p)
	case 3:
		return pos{p.row, 0}, pos{p.row, g.cols}
	}
	return p, p
}

func (v *View) extend(p pos, g grid) {
	a, b := v.unitAt(p, g)
	lo, hi := v.sel.anchor[0], v.sel.anchor[1]
	if a.before(lo) {
		v.sel.a, v.sel.b = a, hi
	} else {
		v.sel.a, v.sel.b = lo, b
	}
	if v.sel.b.before(v.sel.a) {
		v.sel.a, v.sel.b = v.sel.b, v.sel.a
	}
}

func (v *View) selected(row, col int) bool {
	if !v.sel.on {
		return false
	}
	p := pos{row, col}
	return !p.before(v.sel.a) && p.before(v.sel.b)
}

// --- paint ------------------------------------------------------------------

func (v *View) paint(dl *render.DrawList, eng *shape.Engine) {
	f := v.host.Frame
	dl.AddRect(f, v.pal.Bg)
	if v.cellW <= 0 {
		return
	}
	size := v.fontSize(eng)
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	g := v.s.grid()
	// Stay on the same lines while output arrives below them.
	if v.scroll > 0 && g.sb > v.lastSB {
		v.scroll += g.sb - v.lastSB
	}
	v.lastSB = g.sb
	v.scroll = min(max(v.scroll, 0), g.sb)
	top := g.sb - v.scroll
	x0, y0 := f.X+pad, f.Y+pad

	var run strings.Builder
	for r := range g.rows {
		row := top + r
		y := y0 + float32(r)*v.cellH
		runCol, runFg := 0, render.Color{}
		flush := func(col int) {
			if run.Len() > 0 {
				eng.DrawStringTopMonoAt(dl, run.String(), x0+float32(runCol)*v.cellW, y, runFg, size)
				run.Reset()
			}
			runCol = col
		}
		for col := 0; col < g.cols; col++ {
			c := g.cell(row, col)
			fg, bg, attrs := v.colors(c)
			if v.selected(row, col) {
				bg = v.pal.Selection
			}
			w := 1
			if c != nil && c.Width > 1 {
				w = c.Width
			}
			x := x0 + float32(col)*v.cellW
			if bg != v.pal.Bg {
				dl.AddRect(render.Rect{X: x, Y: y, W: float32(w) * v.cellW, H: v.cellH}, bg)
			}
			if c != nil && c.Style.Underline != 0 {
				dl.AddRect(render.Rect{X: x, Y: y + v.cellH - 2, W: float32(w) * v.cellW, H: 1}, fg)
			}
			if attrs&uv.AttrStrikethrough != 0 {
				dl.AddRect(render.Rect{X: x, Y: y + v.cellH/2, W: float32(w) * v.cellW, H: 1}, fg)
			}
			text := ""
			if c != nil && attrs&uv.AttrConceal == 0 {
				text = c.Content
			}
			ascii := len(text) == 1 && text[0] > ' ' && text[0] < utf8.RuneSelf
			switch {
			case text == "" || text == " ":
				flush(col + 1)
			case ascii && (run.Len() == 0 || fg == runFg):
				if run.Len() == 0 {
					runCol, runFg = col, fg
				}
				run.WriteString(text)
			case ascii:
				flush(col)
				runFg = fg
				run.WriteString(text)
			default:
				// Wide and non-ASCII characters go one by one so a fallback
				// font's advance cannot shift the rest of the row.
				flush(col + w)
				eng.DrawStringTopMonoAt(dl, text, x, y, fg, size)
			}
			col += w - 1
		}
		flush(0)
	}
	v.paintCursor(dl, eng, g, top, x0, y0, size)
}

// colors resolves a cell's foreground and background, with reverse video,
// bold-as-bright and faint applied.
func (v *View) colors(c *uv.Cell) (fg, bg render.Color, attrs uint8) {
	if c == nil {
		return v.pal.Fg, v.pal.Bg, 0
	}
	st := c.Style
	attrs = st.Attrs
	fgc := st.Fg
	if bc, ok := fgc.(ansi.BasicColor); ok && bc < 8 && attrs&uv.AttrBold != 0 {
		fgc = bc + 8
	}
	fg = v.pal.color(fgc, v.pal.Fg)
	bg = v.pal.color(st.Bg, v.pal.Bg)
	if attrs&uv.AttrReverse != 0 {
		fg, bg = bg, fg
	}
	if attrs&uv.AttrFaint != 0 {
		fg.A *= 0.6
	}
	return fg, bg, attrs
}

func (v *View) paintCursor(dl *render.DrawList, eng *shape.Engine, g grid, top int, x0, y0 float32, size render.Px) {
	e := v.s.emu
	if v.s.exited || e.CursorHidden() {
		return
	}
	cp := e.CursorPosition()
	row := g.sb + cp.Y - top
	if row < 0 || row >= g.rows || cp.X >= g.cols {
		return
	}
	x, y := x0+float32(cp.X)*v.cellW, y0+float32(row)*v.cellH
	cell := render.Rect{X: x, Y: y, W: v.cellW, H: v.cellH}
	if !v.focused {
		dl.AddRoundedRectBorder(cell, 0, 1, render.Color{}, v.pal.Cursor)
		return
	}
	style, blinks := e.CursorStyle()
	if blinks && time.Since(v.blink)%(2*blinkHalf) >= blinkHalf {
		return
	}
	switch style {
	case vt.CursorBar:
		dl.AddRect(render.Rect{X: x, Y: y, W: 2, H: v.cellH}, v.pal.Cursor)
	case vt.CursorUnderline:
		dl.AddRect(render.Rect{X: x, Y: y + v.cellH - 2, W: v.cellW, H: 2}, v.pal.Cursor)
	default:
		dl.AddRect(cell, v.pal.Cursor)
		if c := g.cell(g.sb+cp.Y, cp.X); c != nil && c.Content != "" && c.Content != " " {
			eng.DrawStringTopMonoAt(dl, c.Content, x, y, v.pal.Bg, size)
		}
	}
}
