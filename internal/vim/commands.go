// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package vim

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

// char handles one typed character outside insert mode.
func (s *State) char(r rune) {
	if s.pending != "" {
		s.pendingChar(r)
		return
	}
	if (r >= '1' && r <= '9') || (r == '0' && s.count != "") {
		s.count += string(r)
		return
	}
	if s.mode == Visual || s.mode == VisualLine {
		s.visualChar(r)
		return
	}
	if s.op != 0 {
		s.opChar(r)
		return
	}
	b, c := s.b.Bytes(), s.b.Caret()
	switch r {
	case 'g', 'f', 't', 'F', 'T', 'r', '"':
		s.pending = string(r)
	case 'd', 'c', 'y', '>', '<':
		s.op, s.opCount, s.count = r, s.count, ""
	case 'x', 'X':
		n := s.takeCount(1)
		s.startChange()
		lo, hi := c, c
		if r == 'x' {
			for i := 0; i < n && hi < lineEnd(b, c); i++ {
				hi = nextRune(b, hi)
			}
		} else {
			for i := 0; i < n && lo > lineStart(b, c); i++ {
				lo = prevRune(b, lo)
			}
		}
		s.apply('d', lo, hi, false)
		s.endChange()
	case 'D', 'C':
		s.op = map[rune]rune{'D': 'd', 'C': 'c'}[r]
		s.opChar('$')
	case 'Y':
		s.op = 'y'
		s.opChar('y')
	case 's':
		s.op = 'c'
		s.opChar('l')
	case 'S':
		s.op = 'c'
		s.opChar('c')
	case 'p', 'P':
		s.startChange()
		s.put(r == 'P', s.takeCount(1))
		s.endChange()
	case 'u':
		n := s.takeCount(1)
		for i := 0; i < n; i++ {
			s.b.Undo()
		}
		s.clampNormal()
	case 'J':
		s.startChange()
		s.join(max(2, s.takeCount(2)))
		s.endChange()
	case '~':
		s.startChange()
		hi := c
		for i, n := 0, s.takeCount(1); i < n && hi < lineEnd(b, c); i++ {
			hi = nextRune(b, hi)
		}
		s.b.Replace(c, hi, toggleCase(string(b[c:hi])))
		s.moveTo(min(hi, max(lineStart(b, c), prevRune(s.b.Bytes(), lineEnd(s.b.Bytes(), c)))), false)
		s.endChange()
	case 'i', 'a', 'I', 'A', 'o', 'O':
		s.startChange()
		s.insertAt(r)
	case 'v', 'V':
		s.mode = map[rune]Mode{'v': Visual, 'V': VisualLine}[r]
		s.anchor, s.visualCaret = c, c
		s.showVisual()
	case '.':
		n := s.takeCount(1)
		for i := 0; i < n; i++ {
			s.repeat()
		}
	case '/', '?', ':':
		s.cmdline = string(r)
	default:
		if to, _, _, ok := s.motion(r, s.takeCount(0)); ok {
			s.moveTo(to, r == 'j' || r == 'k')
		}
		s.reset()
	}
}

func (s *State) pendingChar(r rune) {
	p := s.pending
	s.pending = ""
	b, c := s.b.Bytes(), s.cur()
	switch p {
	case "\"":
		s.regName = r
	case "r":
		s.startChange()
		hi := c
		for i, n := 0, s.takeCount(1); i < n; i++ {
			if hi >= lineEnd(b, c) {
				s.endChange()
				return // not enough characters: Vim does nothing
			}
			hi = nextRune(b, hi)
		}
		n := utf8.RuneCount(b[c:hi])
		s.b.Replace(c, hi, strings.Repeat(string(r), n))
		s.moveTo(prevRune(s.b.Bytes(), c+n*utf8.RuneLen(r)), false)
		s.endChange()
	case "g", "f", "t", "F", "T":
		key := rune(p[0])
		if p == "g" {
			if r != 'g' && r != 'e' && r != 'E' {
				s.reset()
				return
			}
			key = 'G' + 1000 // gg
			if r != 'g' {
				key = map[rune]rune{'e': 'e' + 1000, 'E': 'E' + 1000}[r]
			}
		} else {
			s.lastFind = string(key) + string(r)
		}
		s.applyMotion(key)
	case "i", "a":
		lo, hi, ok := textObject(b, c, p == "a", r)
		if !ok {
			s.reset()
			return
		}
		if s.mode == Visual || s.mode == VisualLine {
			s.mode = Visual
			s.anchor = lo
			s.moveTo(prevRune(b, hi), false)
			return
		}
		op := s.op
		s.op = 0
		s.count, s.opCount = "", ""
		s.apply(op, lo, hi, false)
		s.endChange()
	}
}

// applyMotion finishes a motion typed after a prefix (gg, fx).
func (s *State) applyMotion(key rune) {
	switch {
	case s.mode == Visual || s.mode == VisualLine:
		if to, _, _, ok := s.motion(key, s.takeCount(0)); ok {
			s.moveTo(to, false)
		}
	case s.op != 0:
		s.opMotion(key)
	default:
		if to, _, _, ok := s.motion(key, s.takeCount(0)); ok {
			s.moveTo(to, false)
		}
		s.reset()
	}
}

// opChar takes the key after an operator: the same key again (dd), a text
// object prefix, a motion prefix, or a motion.
func (s *State) opChar(r rune) {
	s.startChange()
	switch {
	case r == s.op:
		n := s.takeCount(1)
		b, c := s.b.Bytes(), s.b.Caret()
		lo := lineStart(b, c)
		hi := lo
		for i := 0; i < n; i++ {
			hi = lineEnd(b, hi)
			if i < n-1 && hi < len(b) {
				hi++
			}
		}
		op := s.op
		s.op = 0
		s.apply(op, lo, hi, true)
		s.endChange()
	case r == 'i' || r == 'a' || r == 'g' || r == 'f' || r == 't' || r == 'F' || r == 'T':
		s.pending = string(r)
	default:
		s.opMotion(r)
	}
}

// opMotion runs the pending operator over a motion.
func (s *State) opMotion(r rune) {
	op := s.op
	s.op = 0
	b, c := s.b.Bytes(), s.b.Caret()
	n := s.takeCount(0)
	if op == 'c' && (r == 'w' || r == 'W') && c < len(b) && class(runeAt(b, c), r == 'W') != 0 {
		r = map[rune]rune{'w': 'e', 'W': 'E'}[r] // cw changes to the end of the word
	}
	to, linewise, inclusive, ok := s.motion(r, n)
	if !ok {
		s.reset()
		s.recording = false
		return
	}
	if (r == 'w' || r == 'W') && to > lineEnd(b, c) && !linewise {
		to = lineEnd(b, c) // dw at the end of a line stays on it
	}
	lo, hi := min(c, to), max(c, to)
	if inclusive {
		hi = nextRune(b, hi)
	}
	s.apply(op, lo, hi, linewise)
	s.endChange()
}

// motion is where key moves the caret count times (0 means no count), and
// how an operator takes it.
func (s *State) motion(key rune, count int) (to int, linewise, inclusive, ok bool) {
	b, c := s.b.Bytes(), s.cur()
	n := max(count, 1)
	ok = true
	to = c
	switch key {
	case 'h':
		for i := 0; i < n && to > lineStart(b, c); i++ {
			to = prevRune(b, to)
		}
	case 'l', ' ':
		limit := lineEnd(b, c)
		if s.op == 0 && s.mode == Normal {
			limit = max(lineStart(b, c), prevRune(b, limit))
		}
		for i := 0; i < n && to < limit; i++ {
			to = nextRune(b, to)
		}
	case 'j', 'k':
		if key == 'k' {
			n = -n
		}
		to, linewise = s.lineMotion(c, n), true
	case 'w', 'W':
		for i := 0; i < n; i++ {
			to = wordForward(b, to, key == 'W')
		}
	case 'b', 'B':
		for i := 0; i < n; i++ {
			to = wordBack(b, to, key == 'B')
		}
	case 'e', 'E':
		for i := 0; i < n; i++ {
			to = wordEnd(b, to, key == 'E')
		}
		inclusive = true
	case 'e' + 1000, 'E' + 1000: // ge, gE
		for i := 0; i < n; i++ {
			to = wordBack(b, to, key == 'E'+1000)
			if to > 0 {
				to = wordEnd(b, prevRune(b, wordBack(b, to, key == 'E'+1000)), key == 'E'+1000)
			}
		}
		inclusive = true
	case '0':
		to = lineStart(b, c)
	case '^':
		to = firstNonBlank(b, c)
	case '$':
		ln := c
		for i := 1; i < n; i++ {
			if e := lineEnd(b, ln); e < len(b) {
				ln = e + 1
			}
		}
		to = lineEnd(b, ln)
		if s.op == 0 {
			to = max(lineStart(b, ln), prevRune(b, to))
			inclusive = true
		}
		s.goalCol = 1 << 30
	case 'G', 'G' + 1000: // G, gg
		ln := lineIndex(b, len(b))
		if key == 'G'+1000 {
			ln = 0
		}
		if count > 0 {
			ln = count - 1
		}
		to, linewise = firstNonBlank(b, lineStartN(b, ln)), true
	case '{', '}':
		dir := map[rune]int{'{': -1, '}': 1}[key]
		for i := 0; i < n; i++ {
			to = paragraph(b, to, dir)
		}
	case '%':
		to, ok = matchPair(b, c)
		inclusive = true
	case 'f', 't', 'F', 'T', ';', ',':
		// f and friends arrive here through pending, with lastFind set.
		if len(s.lastFind) == 0 {
			return c, false, false, false
		}
		kind, ch := rune(s.lastFind[0]), []rune(s.lastFind[1:])[0]
		if key == ',' {
			kind = map[rune]rune{'f': 'F', 'F': 'f', 't': 'T', 'T': 't'}[kind]
		}
		repeat := key == ';' || key == ','
		for i := 0; i < n; i++ {
			next, found := findInLine(b, to, kind, ch, repeat || i > 0)
			if !found {
				return c, false, false, i > 0
			}
			to = next
		}
		inclusive = kind == 'f' || kind == 't'
	case 'n', 'N':
		to, ok = s.findSearch(c, key == 'N')
	case '*', '#':
		lo, hi, found := textObject(b, c, false, 'w')
		if !found || lo == hi {
			return c, false, false, false
		}
		s.search = string(b[lo:hi])
		to, ok = s.findSearch(c, key == '#')
	default:
		return c, false, false, false
	}
	if key != 'j' && key != 'k' && key != '$' {
		s.goalCol = -1
	}
	return to, linewise, inclusive, ok
}

// lineMotion moves n lines (negative is up), aiming for the goal column.
func (s *State) lineMotion(c, n int) int {
	b := s.b.Bytes()
	col := s.goalCol
	if col < 0 {
		col = utf8.RuneCount(b[lineStart(b, c):c])
		s.goalCol = col
	}
	ln := max(0, lineIndex(b, c)+n)
	ls := lineStartN(b, ln)
	le := lineEnd(b, ls)
	to := ls
	for i := 0; i < col && to < le; i++ {
		to = nextRune(b, to)
	}
	if to == le && to > ls && s.mode == Normal && s.op == 0 {
		to = prevRune(b, to)
	}
	return to
}

func (s *State) findSearch(c int, back bool) (int, bool) {
	if s.search == "" {
		return c, false
	}
	b := s.b.Bytes()
	needle := []byte(s.search)
	if !back {
		if i := bytes.Index(b[min(len(b), c+1):], needle); i >= 0 {
			return c + 1 + i, true
		}
		if i := bytes.Index(b, needle); i >= 0 {
			return i, true // wrap around
		}
		return c, false
	}
	if i := bytes.LastIndex(b[:c], needle); i >= 0 {
		return i, true
	}
	if i := bytes.LastIndex(b, needle); i >= 0 {
		return i, true
	}
	return c, false
}

func (s *State) searchNext(back bool) {
	if to, ok := s.findSearch(s.b.Caret(), back); ok {
		s.moveTo(to, false)
	}
}

// moveTo moves the caret; keepGoal keeps the column j and k aim for.
func (s *State) moveTo(to int, keepGoal bool) {
	if !keepGoal {
		s.goalCol = -1
	}
	to = max(0, min(to, len(s.b.Bytes())))
	if s.mode == Visual || s.mode == VisualLine {
		s.visualCaret = to
		s.showVisual()
		return
	}
	s.b.SetSelection(to, to)
	s.clampNormal()
}

// apply runs operator op over [lo, hi).
func (s *State) apply(op rune, lo, hi int, linewise bool) {
	b := s.b.Bytes()
	if linewise {
		lo = lineStart(b, lo)
		if e := lineEnd(b, max(lo, hi-1)); e < len(b) {
			hi = e + 1
		} else {
			hi = len(b)
			if op == 'd' && lo > 0 {
				lo-- // the last line takes the newline before it
			}
		}
	}
	text := string(b[lo:hi])
	switch op {
	case 'y':
		s.setReg(text, linewise, true)
		s.b.SetSelection(lo, lo)
		if linewise {
			s.b.SetSelection(s.b.Caret(), s.b.Caret())
		}
	case 'd':
		s.setReg(text, linewise, false)
		s.b.UndoGroup(func() { s.b.Replace(lo, hi, "") })
		nb := s.b.Bytes()
		at := min(lo, len(nb))
		if linewise {
			if at < len(nb) && lo > 0 && b[lo] == '\n' {
				at++ // deleted the last line: go to the line above's start
			}
			at = firstNonBlank(nb, min(at, len(nb)))
		}
		s.b.SetSelection(at, at)
		s.clampNormal()
	case 'c':
		s.setReg(text, linewise, false)
		if linewise {
			// Keep the line, and its indent, to type over.
			ind := b[lo:firstNonBlank(b, lo)]
			end := hi
			if end > lo && end <= len(b) && b[end-1] == '\n' {
				end--
			}
			s.b.Replace(lo, end, string(ind))
		} else {
			s.b.Replace(lo, hi, "")
		}
		s.enterInsert()
	case '>', '<':
		s.indent(lo, hi, op == '>')
	case '~':
		s.b.Replace(lo, hi, toggleCase(text))
		s.b.SetSelection(lo, lo)
	}
	s.reset()
}

func (s *State) setReg(text string, linewise, yank bool) {
	r := register{text: text, linewise: linewise}
	name := s.regName
	switch {
	case name >= 'a' && name <= 'z':
		s.regs[name] = r
	case name >= 'A' && name <= 'Z':
		prev := s.regs[unicode.ToLower(name)]
		s.regs[unicode.ToLower(name)] = register{text: prev.text + text, linewise: prev.linewise || linewise}
	}
	if name != '_' {
		s.regs['"'] = r
		if yank {
			s.regs['0'] = r
		}
		if s.host != nil {
			s.host.Yank(text)
		}
	}
	s.regName = 0
}

// put puts the register after (or before) the caret, count times.
func (s *State) put(before bool, n int) {
	name := s.regName
	if name == 0 {
		name = '"'
	}
	s.regName = 0
	reg, ok := s.regs[unicode.ToLower(name)]
	if !ok || reg.text == "" {
		return
	}
	text := strings.Repeat(reg.text, n)
	b, c := s.b.Bytes(), s.b.Caret()
	if reg.linewise {
		at := lineStart(b, c)
		if !before {
			at = lineEnd(b, c)
			if at < len(b) {
				at++
			} else {
				text = "\n" + strings.TrimSuffix(text, "\n")
			}
		}
		if !strings.HasSuffix(text, "\n") && at < len(b) {
			text += "\n"
		}
		s.b.Replace(at, at, text)
		land := at
		if at > 0 && text[0] == '\n' {
			land++
		}
		s.moveTo(firstNonBlank(s.b.Bytes(), land), false)
		return
	}
	at := c
	if !before && c < lineEnd(b, c) {
		at = nextRune(b, c)
	}
	s.b.Replace(at, at, text)
	s.moveTo(prevRune(s.b.Bytes(), at+len(text)), false)
}

// join joins n lines into one, with a space between (J).
func (s *State) join(n int) {
	s.b.UndoGroup(func() {
		for i := 1; i < n; i++ {
			b, c := s.b.Bytes(), s.b.Caret()
			e := lineEnd(b, c)
			if e >= len(b) {
				return
			}
			next := firstNonBlank(b, e+1)
			sep := " "
			if e == lineStart(b, e) || next >= len(b) || b[next] == '\n' || b[next] == ')' {
				sep = ""
			}
			// Trailing blanks of this line go too.
			lo := e
			for lo > lineStart(b, e) && (b[lo-1] == ' ' || b[lo-1] == '\t') {
				lo--
			}
			s.b.Replace(lo, next, sep)
			s.b.SetSelection(lo, lo)
		}
	})
}

func (s *State) indent(lo, hi int, right bool) {
	b := s.b.Bytes()
	s.b.UndoGroup(func() {
		// Work bottom-up so offsets above stay put.
		var starts []int
		for ls := lineStart(b, lo); ls < hi || ls == lineStart(b, lo); {
			starts = append(starts, ls)
			e := lineEnd(b, ls)
			if e >= len(b) {
				break
			}
			ls = e + 1
		}
		for i := len(starts) - 1; i >= 0; i-- {
			ls := starts[i]
			cur := s.b.Bytes()
			if right {
				if lineEnd(cur, ls) > ls {
					s.b.Replace(ls, ls, "\t")
				}
				continue
			}
			switch {
			case ls < len(cur) && cur[ls] == '\t':
				s.b.Replace(ls, ls+1, "")
			default:
				k := ls
				for k < len(cur) && k-ls < 4 && cur[k] == ' ' {
					k++
				}
				s.b.Replace(ls, k, "")
			}
		}
	})
	nb := s.b.Bytes()
	at := firstNonBlank(nb, min(lineStart(b, lo), len(nb)))
	s.b.SetSelection(at, at)
}

// insertAt enters insert mode where i, a, I, A, o or O puts the caret.
func (s *State) insertAt(r rune) {
	b, c := s.b.Bytes(), s.b.Caret()
	at := c
	switch r {
	case 'a':
		if c < lineEnd(b, c) {
			at = nextRune(b, c)
		}
	case 'I':
		at = firstNonBlank(b, c)
	case 'A':
		at = lineEnd(b, c)
	case 'o', 'O':
		ls := lineStart(b, c)
		ind := string(b[ls:firstNonBlank(b, c)])
		if r == 'o' {
			e := lineEnd(b, c)
			s.b.Replace(e, e, "\n"+ind)
			at = e + 1 + len(ind)
		} else {
			s.b.Replace(ls, ls, ind+"\n")
			at = ls + len(ind)
		}
	}
	s.count = ""
	s.b.SetSelection(at, at)
	s.enterInsert()
}

func toggleCase(t string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsUpper(r) {
			return unicode.ToLower(r)
		}
		return unicode.ToUpper(r)
	}, t)
}

// visualChar handles a character in visual mode.
func (s *State) visualChar(r rune) {
	switch r {
	case 'v', 'V':
		m := map[rune]Mode{'v': Visual, 'V': VisualLine}[r]
		if s.mode == m {
			s.escape()
			return
		}
		s.mode = m
		s.showVisual()
	case 'o':
		s.anchor, s.visualCaret = s.visualCaret, s.anchor
		s.showVisual()
	case 'g', 'f', 't', 'F', 'T', 'i', 'a', '"':
		s.pending = string(r)
	case 'd', 'x', 'y', 'c', 's', '>', '<', '~', 'D', 'X', 'Y', 'C', 'S', 'J':
		lo, hi, linewise := s.visualRange()
		op := map[rune]rune{'x': 'd', 's': 'c', 'D': 'd', 'X': 'd', 'Y': 'y', 'C': 'c', 'S': 'c'}[r]
		if op == 0 {
			op = r
		}
		if r == 'D' || r == 'X' || r == 'Y' || r == 'C' || r == 'S' {
			linewise = true
		}
		s.mode = Normal
		s.startChange()
		if r == 'J' {
			b := s.b.Bytes()
			s.b.SetSelection(lo, lo)
			s.join(max(2, lineIndex(b, hi-1)-lineIndex(b, lo)+1))
		} else {
			s.apply(op, lo, hi, linewise)
		}
		s.endChange()
		if s.mode == Normal {
			s.clampNormal()
		}
	case 'p', 'P':
		lo, hi, linewise := s.visualRange()
		reg := s.regs['"']
		s.mode = Normal
		s.b.UndoGroup(func() {
			s.b.Replace(lo, hi, "")
			s.b.SetSelection(lo, lo)
			if linewise && !reg.linewise {
				s.b.Replace(lo, lo, "\n")
			}
			s.regs['"'] = reg
			s.put(true, 1)
		})
	case 'I', 'A':
		lo, hi, _ := s.visualRange()
		s.mode = Normal
		at := lo
		if r == 'A' {
			at = hi
		}
		s.b.SetSelection(at, at)
		s.startChange()
		s.enterInsert()
	default:
		if to, _, _, ok := s.motion(r, s.takeCount(0)); ok {
			s.moveTo(to, r == 'j' || r == 'k')
		}
	}
}

// visualRange is the selected range, whole lines in visual-line mode.
func (s *State) visualRange() (lo, hi int, linewise bool) {
	b := s.b.Bytes()
	lo, hi = min(s.anchor, s.visualCaret), max(s.anchor, s.visualCaret)
	hi = nextRune(b, hi)
	if s.mode == VisualLine {
		return lineStart(b, lo), lineEnd(b, max(lo, hi-1)), true
	}
	return lo, min(hi, len(b)), false
}

// showVisual selects the visual range in the editor, the caret at the end
// that moves.
func (s *State) showVisual() {
	lo, hi, _ := s.visualRange()
	if s.visualCaret < s.anchor {
		s.b.SetSelection(hi, lo)
	} else {
		s.b.SetSelection(lo, hi)
	}
}
