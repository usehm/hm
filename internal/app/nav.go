package app

// navLoc is a place the caret was: a file and a byte offset in it.
type navLoc struct {
	path      string
	off, line int
}

// navHistory holds the places to go back and forward to. A place is kept
// when the caret leaves it for another file or by navJump lines or more:
// a jump to a definition, a search result, a click far away.
type navHistory struct {
	back, fwd []navLoc
	last      navLoc
}

const (
	navJump = 10  // lines that count as leaving a place
	navMax  = 100 // places kept each way
)

// trackNav records where the caret is; call once per frame.
func (a *App) trackNav() {
	ed := a.ws.ActiveEditor()
	if ed == nil {
		return
	}
	line, _ := ed.LineCol(ed.Caret())
	cur := navLoc{path: ed.Path, off: ed.Caret(), line: line}
	n := &a.nav
	if last := n.last; last.path != "" && (last.path != cur.path || abs(last.line-cur.line) >= navJump) {
		n.back = pushNav(n.back, last)
		n.fwd = n.fwd[:0]
	}
	n.last = cur
}

func pushNav(s []navLoc, l navLoc) []navLoc {
	if k := len(s); k > 0 && s[k-1] == l {
		return s
	}
	s = append(s, l)
	if len(s) > navMax {
		s = s[len(s)-navMax:]
	}
	return s
}

// navBack goes to the previous place.
func (a *App) navBack() {
	n := &a.nav
	if len(n.back) == 0 {
		return
	}
	to := n.back[len(n.back)-1]
	n.back = n.back[:len(n.back)-1]
	n.fwd = pushNav(n.fwd, n.last)
	a.navGo(to)
}

// navForward goes to the place navBack left.
func (a *App) navForward() {
	n := &a.nav
	if len(n.fwd) == 0 {
		return
	}
	to := n.fwd[len(n.fwd)-1]
	n.fwd = n.fwd[:len(n.fwd)-1]
	n.back = pushNav(n.back, n.last)
	a.navGo(to)
}

// navGo shows l without recording the move as a new place.
func (a *App) navGo(l navLoc) {
	a.openFile(l.path)
	ed := a.bufs.Get(l.path)
	if ed == nil {
		return
	}
	ed.SetSelection(l.off, l.off)
	ed.CenterCaret()
	ed.Focus()
	line, _ := ed.LineCol(ed.Caret())
	a.nav.last = navLoc{path: l.path, off: ed.Caret(), line: line}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
