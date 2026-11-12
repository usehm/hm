// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package vim

import (
	"strings"
	"testing"

	"github.com/mirzakhany/yoga/input"
)

// buf is a Buffer for tests: text with a caret and whole-text undo.
type buf struct {
	text          string
	anchor, caret int
	undo, redo    []string
	group         bool
}

func (b *buf) Bytes() []byte { return []byte(b.text) }
func (b *buf) Caret() int    { return b.caret }
func (b *buf) SetSelection(anchor, caret int) {
	b.anchor, b.caret = anchor, caret
}
func (b *buf) Replace(lo, hi int, t string) {
	if !b.group {
		b.undo = append(b.undo, b.text)
	}
	b.redo = nil
	b.text = b.text[:lo] + t + b.text[hi:]
	b.anchor, b.caret = lo+len(t), lo+len(t)
}
func (b *buf) UndoGroup(fn func()) {
	if b.group {
		fn()
		return
	}
	b.undo = append(b.undo, b.text)
	b.group = true
	fn()
	b.group = false
}
func (b *buf) Undo() {
	if n := len(b.undo); n > 0 {
		b.redo = append(b.redo, b.text)
		b.text = b.undo[n-1]
		b.undo = b.undo[:n-1]
		b.caret = min(b.caret, len(b.text))
	}
}
func (b *buf) Redo() {
	if n := len(b.redo); n > 0 {
		b.undo = append(b.undo, b.text)
		b.text = b.redo[n-1]
		b.redo = b.redo[:n-1]
	}
}

type host struct{ ex []string }

func (h *host) Ex(cmd string) bool { h.ex = append(h.ex, cmd); return true }
func (h *host) Yank(string)        {}

// feed types keys: "<esc>", "<cr>", "<bs>" are keys, the rest is text that
// an editor in insert mode would type itself.
func feed(s *State, b *buf, keys string) {
	for keys != "" {
		var ev *input.KeyEvent
		for name, k := range map[string]input.Key{"<esc>": input.KeyEscape, "<cr>": input.KeyEnter, "<bs>": input.KeyBackspace} {
			if strings.HasPrefix(keys, name) {
				ev = &input.KeyEvent{Key: k}
				keys = keys[len(name):]
				break
			}
		}
		if ev != nil {
			if !s.Key(*ev) {
				s.insertKey(*ev) // the editor would act on it
			}
			continue
		}
		r := []rune(keys)[0]
		keys = keys[len(string(r)):]
		if !s.Text([]rune{r}) {
			b.Replace(b.caret, b.caret, string(r))
		}
	}
}

func TestVimCommands(t *testing.T) {
	cases := []struct {
		name, text, keys, want string
		caret                  int // -1: don't check
	}{
		{"x", "hello", "x", "ello", 0},
		{"count x", "hello", "3x", "lo", 0},
		{"dw", "foo bar baz", "dw", "bar baz", 0},
		{"d2w", "foo bar baz", "d2w", "baz", 0},
		{"2dw", "foo bar baz", "2dw", "baz", 0},
		{"cw", "foo bar", "cwxy<esc>", "xy bar", 1},
		{"dd", "a\nb\nc", "jdd", "a\nc", 2},
		{"dd last line", "a\nb", "jdd", "a", 0},
		{"2dd", "a\nb\nc", "2dd", "c", 0},
		{"yy p", "a\nb", "yyp", "a\na\nb", 2},
		{"yy P", "a\nb", "jyyP", "a\nb\nb", 2},
		{"yw P", "foo bar", "ywP", "foo foo bar", 3},
		{"D", "hello world", "wD", "hello ", 5},
		{"C", "hello world", "wCthere<esc>", "hello there", 10},
		{"A", "abc", "Ad<esc>", "abcd", 3},
		{"I", "  abc", "$Ix<esc>", "  xabc", 2},
		{"o", "  a\nb", "ox<esc>", "  a\n  x\nb", 6},
		{"O", "a", "Ox<esc>", "x\na", 0},
		{"ciw", "foo bar baz", "wciwX<esc>", "foo X baz", 4},
		{"diw", "foo bar", "diw", " bar", 0},
		{"daw", "foo bar baz", "wdaw", "foo baz", 4},
		{`ci"`, `say "hi there" now`, `fhci"yo<esc>`, `say "yo" now`, 6},
		{"di(", "f(a, b) + 1", "fadi(", "f() + 1", 2},
		{"da(", "f(a, b) + 1", "fada(", "f + 1", 1},
		{"dt", "a,b,c", "dt,", ",b,c", 0},
		{"df", "a,b,c", "df,", "b,c", 0},
		{"; repeat", "a,b,c,d", "f,;x", "a,bc,d", 3},
		{"r", "abc", "rX", "Xbc", 0},
		{"~", "abc", "~~", "ABc", 2},
		{"J", "a\n  b\nc", "J", "a b\nc", 1},
		{"dot", "a b c d", "dw..", "d", 0},
		{"dot insert", "x\nx", "Ay<esc>j.", "xy\nxy", 4},
		{"u", "abc", "xxu", "bc", -1},
		{"G gg", "a\nb\nc", "Gdd", "a\nb", 2},
		{"gg", "a\nb\nc", "Gggdd", "b\nc", 0},
		{"$", "abc", "$x", "ab", 1},
		{">>", "a", ">>", "\ta", 1},
		{"<<", "\t\ta", "<<", "\ta", 1},
		{"%", "(a (b) c)", "%x", "(a (b) c", 7},
		{"visual d", "hello world", "vlld", "lo world", 0},
		{"visual line y P", "a\nb", "VyjP", "a\na\nb", 2},
		{"visual iw c", "foo bar", "wviwcX<esc>", "foo X", 4},
		{"visual o", "abcdef", "llvlohd", "aef", 1},
		{"search", "one two one", "/one<cr>x", "one two ne", 8},
		{"star", "foo bar foo", "*x", "foo bar oo", 8},
		{"register", "a b", `"adwwd$"aP`, "a ", -1},
		{"ex line", "a\nb\nc", ":3<cr>x", "a\nb\n", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &buf{text: tc.text}
			s := New(b, &host{})
			feed(s, b, tc.keys)
			if b.text != tc.want || (tc.caret >= 0 && b.caret != tc.caret) {
				t.Fatalf("%q + %q = %q caret %d, want %q caret %d", tc.text, tc.keys, b.text, b.caret, tc.want, tc.caret)
			}
			if s.Mode() != Normal {
				t.Fatalf("mode %v after %q", s.Mode(), tc.keys)
			}
		})
	}
}

func TestVimMotions(t *testing.T) {
	cases := []struct {
		text, keys string
		caret      int
	}{
		{"foo bar", "w", 4},
		{"foo.bar baz", "w", 3},
		{"foo.bar baz", "W", 8},
		{"foo bar", "e", 2},
		{"foo bar", "we", 6},
		{"foo bar", "$b", 4},
		{"  ab", "^", 2},
		{"ab\ncd", "jk", 0},
		{"abc\nd\nefg", "$jj", 8},
		{"a\n\nb", "}", 2},
		{"abc", "lll", 2},
	}
	for _, tc := range cases {
		b := &buf{text: tc.text}
		s := New(b, &host{})
		feed(s, b, tc.keys)
		if b.caret != tc.caret {
			t.Errorf("%q + %q: caret %d, want %d", tc.text, tc.keys, b.caret, tc.caret)
		}
	}
}

func TestVimExAndStatus(t *testing.T) {
	b := &buf{text: "a"}
	h := &host{}
	s := New(b, h)
	feed(s, b, ":w")
	if s.Status() != ":w" {
		t.Fatalf("status %q", s.Status())
	}
	feed(s, b, "q<cr>")
	if len(h.ex) != 1 || h.ex[0] != "wq" {
		t.Fatalf("ex %v", h.ex)
	}
	feed(s, b, "2d")
	if s.Status() != "-- NORMAL --  2d" {
		t.Fatalf("status %q", s.Status())
	}
	feed(s, b, "<esc>i")
	if s.Mode() != Insert || s.Status() != "-- INSERT --" {
		t.Fatalf("status %q", s.Status())
	}
}

func TestVimBatchedText(t *testing.T) {
	b := &buf{text: "abc"}
	h := &host{}
	s := New(b, h)
	s.Text([]rune("iXY"))
	s.Key(input.KeyEvent{Key: input.KeyEscape})
	s.Text([]rune(":w"))
	s.Key(input.KeyEvent{Key: input.KeyEnter})
	if b.text != "XYabc" || len(h.ex) != 1 || h.ex[0] != "w" {
		t.Fatalf("text %q ex %v", b.text, h.ex)
	}
}
