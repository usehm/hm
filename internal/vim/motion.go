// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package vim

import (
	"unicode"
	"unicode/utf8"
)

// Text helpers over the document's bytes. Offsets are byte offsets.

func lineStart(b []byte, off int) int {
	for off > 0 && b[off-1] != '\n' {
		off--
	}
	return off
}

// lineEnd is the offset of the line's '\n', or len(b).
func lineEnd(b []byte, off int) int {
	for off < len(b) && b[off] != '\n' {
		off++
	}
	return off
}

// lineStartN is the start of line n (0-based), clamped to the last line.
func lineStartN(b []byte, n int) int {
	off := 0
	for i := 0; i < n; i++ {
		e := lineEnd(b, off)
		if e >= len(b) {
			return lineStart(b, e)
		}
		off = e + 1
	}
	return off
}

func lineIndex(b []byte, off int) int {
	n := 0
	for i := 0; i < off && i < len(b); i++ {
		if b[i] == '\n' {
			n++
		}
	}
	return n
}

func firstNonBlank(b []byte, off int) int {
	i := lineStart(b, off)
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return i
}

func prevRune(b []byte, off int) int {
	if off <= 0 {
		return 0
	}
	_, n := utf8.DecodeLastRune(b[:off])
	return off - n
}

func nextRune(b []byte, off int) int {
	if off >= len(b) {
		return len(b)
	}
	_, n := utf8.DecodeRune(b[off:])
	return off + n
}

func runeAt(b []byte, off int) rune {
	if off < 0 || off >= len(b) {
		return '\n'
	}
	r, _ := utf8.DecodeRune(b[off:])
	return r
}

// class sorts characters for word motions: space, word characters,
// punctuation. Big words (W, B, E) only tell space from the rest.
func class(r rune, big bool) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case big || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 1
	}
	return 2
}

// wordForward is the start of the next word (w, W).
func wordForward(b []byte, off int, big bool) int {
	if off >= len(b) {
		return off
	}
	c := class(runeAt(b, off), big)
	i := off
	for i < len(b) && class(runeAt(b, i), big) == c && c != 0 {
		i = nextRune(b, i)
	}
	// Skip spaces, stopping at an empty line.
	for i < len(b) && class(runeAt(b, i), big) == 0 {
		if b[i] == '\n' && i+1 < len(b) && b[i+1] == '\n' {
			return i + 1
		}
		i = nextRune(b, i)
	}
	return i
}

// wordEnd is the end of this or the next word (e, E), on its last character.
func wordEnd(b []byte, off int, big bool) int {
	i := nextRune(b, off)
	for i < len(b) && class(runeAt(b, i), big) == 0 {
		i = nextRune(b, i)
	}
	if i >= len(b) {
		return prevRune(b, len(b))
	}
	c := class(runeAt(b, i), big)
	for {
		n := nextRune(b, i)
		if n >= len(b) || class(runeAt(b, n), big) != c {
			return i
		}
		i = n
	}
}

// wordBack is the start of this or the previous word (b, B).
func wordBack(b []byte, off int, big bool) int {
	i := prevRune(b, off)
	for i > 0 && class(runeAt(b, i), big) == 0 {
		i = prevRune(b, i)
	}
	c := class(runeAt(b, i), big)
	for i > 0 {
		p := prevRune(b, i)
		if class(runeAt(b, p), big) != c {
			break
		}
		i = p
	}
	return i
}

// paragraph moves to the next (dir 1) or previous (-1) empty line.
func paragraph(b []byte, off, dir int) int {
	ln := lineStart(b, off)
	for {
		if dir > 0 {
			e := lineEnd(b, ln)
			if e >= len(b) {
				return len(b)
			}
			ln = e + 1
		} else {
			if ln == 0 {
				return 0
			}
			ln = lineStart(b, ln-1)
		}
		if lineEnd(b, ln) == ln {
			return ln
		}
	}
}

var pairs = map[rune]rune{'(': ')', '[': ']', '{': '}', '<': '>'}

// matchPair is the bracket matching the one at or after off on its line (%).
func matchPair(b []byte, off int) (int, bool) {
	end := lineEnd(b, off)
	for i := off; i < end; i = nextRune(b, i) {
		r := runeAt(b, i)
		for open, close := range pairs {
			switch r {
			case open:
				if j, ok := findClose(b, i, open, close); ok {
					return j, true
				}
			case close:
				if j, ok := findOpen(b, i, open, close); ok {
					return j, true
				}
			}
		}
	}
	return off, false
}

func findClose(b []byte, from int, open, close rune) (int, bool) {
	depth := 0
	for i := from; i < len(b); i = nextRune(b, i) {
		switch runeAt(b, i) {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// findOpen is the open bracket enclosing from, looking back; a close
// bracket at from itself is the one being matched.
func findOpen(b []byte, from int, open, close rune) (int, bool) {
	depth := 0
	for i := from; ; i = prevRune(b, i) {
		switch r := runeAt(b, i); {
		case r == close && i != from:
			depth++
		case r == open:
			if depth == 0 {
				return i, true
			}
			depth--
		}
		if i == 0 {
			return 0, false
		}
	}
}

// findInLine finds r on the line from off: f/t forward, F/T backward; t and
// T stop next to it. repeat is for ; and , which step past an r right next
// to the caret instead of finding it again.
func findInLine(b []byte, off int, kind, r rune, repeat bool) (int, bool) {
	ls, le := lineStart(b, off), lineEnd(b, off)
	switch kind {
	case 'f', 't':
		for i := nextRune(b, off); i < le; i = nextRune(b, i) {
			if repeat && kind == 't' && i == nextRune(b, off) && runeAt(b, i) == r {
				continue // t repeated next to the char moves on
			}
			if runeAt(b, i) == r {
				if kind == 't' {
					return prevRune(b, i), true
				}
				return i, true
			}
		}
	case 'F', 'T':
		for i := prevRune(b, off); i >= ls && off > ls; i = prevRune(b, i) {
			if repeat && kind == 'T' && i == prevRune(b, off) && runeAt(b, i) == r {
				if i == ls {
					break
				}
				continue
			}
			if runeAt(b, i) == r {
				if kind == 'T' {
					return nextRune(b, i), true
				}
				return i, true
			}
			if i == ls {
				break
			}
		}
	}
	return off, false
}

// textObject is the range of a text object such as iw, a", i( around off.
func textObject(b []byte, off int, around bool, obj rune) (lo, hi int, ok bool) {
	switch obj {
	case 'w', 'W':
		big := obj == 'W'
		if off >= len(b) {
			return 0, 0, false
		}
		c := class(runeAt(b, off), big)
		lo, hi = off, off
		for lo > 0 && class(runeAt(b, prevRune(b, lo)), big) == c && runeAt(b, prevRune(b, lo)) != '\n' {
			lo = prevRune(b, lo)
		}
		for hi < len(b) && class(runeAt(b, hi), big) == c && b[hi] != '\n' {
			hi = nextRune(b, hi)
		}
		if around {
			for hi < len(b) && (b[hi] == ' ' || b[hi] == '\t') {
				hi++
			}
		}
		return lo, hi, true
	case '"', '\'', '`':
		ls, le := lineStart(b, off), lineEnd(b, off)
		var quotes []int
		for i := ls; i < le; i++ {
			if rune(b[i]) == obj && (i == ls || b[i-1] != '\\') {
				quotes = append(quotes, i)
			}
		}
		for k := 0; k+1 < len(quotes); k += 2 {
			if q1, q2 := quotes[k], quotes[k+1]; off <= q2 && (off >= q1 || k == 0) {
				if around {
					return q1, q2 + 1, true
				}
				return q1 + 1, q2, true
			}
		}
		return 0, 0, false
	}
	open, close := obj, rune(0)
	switch obj {
	case 'b', '(', ')':
		open, close = '(', ')'
	case 'B', '{', '}':
		open, close = '{', '}'
	case '[', ']':
		open, close = '[', ']'
	case '<', '>':
		open, close = '<', '>'
	default:
		return 0, 0, false
	}
	o, ok := findOpen(b, off, open, close)
	if !ok || runeAt(b, off) == open {
		o, ok = off, runeAt(b, off) == open
	}
	if !ok {
		return 0, 0, false
	}
	c, ok := findClose(b, o, open, close)
	if !ok {
		return 0, 0, false
	}
	if around {
		return o, c + 1, true
	}
	lo, hi = o+1, c
	// A block on its own lines: inside means the lines between.
	if lo < len(b) && b[lo] == '\n' && lineStart(b, hi) != lineStart(b, lo) {
		lo++
		hi = lineStart(b, hi)
	}
	return lo, hi, true
}
