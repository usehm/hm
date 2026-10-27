// Package vim is a Vim keymap for an editor: normal, insert, visual and
// visual-line modes, counts, operators with motions and text objects,
// registers, repeat with ".", search with "/" and a few ex commands. It is a
// state machine over Buffer, which *ui.Editor implements; the app feeds it
// keys and typed text through the editor's KeyFilter and TextFilter.
package vim

import (
	"strings"

	"github.com/mirzakhany/yoga/input"
)

// Buffer is the editor the keymap drives.
type Buffer interface {
	Bytes() []byte
	Caret() int
	SetSelection(anchor, caret int)
	Replace(lo, hi int, text string)
	UndoGroup(fn func())
	Undo()
	Redo()
}

// Mode is the keymap's mode.
type Mode int

const (
	Normal Mode = iota
	Insert
	Visual
	VisualLine
)

func (m Mode) String() string {
	switch m {
	case Insert:
		return "INSERT"
	case Visual:
		return "VISUAL"
	case VisualLine:
		return "VISUAL LINE"
	}
	return "NORMAL"
}

// Host is what the keymap asks of the app.
type Host interface {
	// Ex runs an ex command line such as "w", "q" or "wq", without the
	// colon, and reports whether it knew it. Line numbers are handled here.
	Ex(cmd string) bool
	// Yank is told what was yanked or deleted, for the system clipboard.
	Yank(text string)
}

// register is yanked text; linewise text is whole lines, put as lines.
type register struct {
	text     string
	linewise bool
}

// State is one editor's keymap.
type State struct {
	b    Buffer
	host Host
	mode Mode

	count       string // digits typed before a command
	opCount     string // the count typed before an operator
	op          rune   // pending operator: d c y > <, or 0
	pending     string // multi-key prefix: g, f F t T r, i a (text objects), "x register
	regName     rune   // register chosen with "x, or 0
	anchor      int    // visual mode's fixed end
	visualCaret int    // visual mode's moving end; the editor's caret sits past it
	goalCol     int    // column j and k aim for; -1 none
	lastFind    string // last f/F/t/T and its char, for ; and ,
	search      string // last search pattern
	cmdline     string // ":" or "/" line being typed, with its prefix; "" when none

	regs map[rune]register

	// Repeat: the input of the last change, replayed by ".". cmdKeys is
	// the command being typed; a change keeps it, plus what an insert it
	// starts types, in rec, and on finishing in last.
	cmdKeys    []event
	recording  bool
	rec, last  []event
	replaying  bool
	insertFrom int // where the current insert began
}

// event is one recorded key or piece of typed text.
type event struct {
	key  input.KeyEvent
	text []rune
}

// New starts a keymap on b in normal mode.
func New(b Buffer, host Host) *State {
	return &State{b: b, host: host, goalCol: -1, regs: map[rune]register{}}
}

// Mode is the current mode.
func (s *State) Mode() Mode { return s.mode }

// cur is where commands act from: the caret, or in visual mode the end of
// the selection that moves (the editor's caret sits just past it).
func (s *State) cur() int {
	if s.mode == Visual || s.mode == VisualLine {
		return s.visualCaret
	}
	return s.b.Caret()
}

// Status is what a status bar shows: the mode, then a command line or the
// keys of a command still being typed.
func (s *State) Status() string {
	if s.cmdline != "" {
		return s.cmdline
	}
	st := "-- " + s.mode.String() + " --"
	if p := s.opCount + string(nonZero(s.op)) + s.count + s.pending; p != "" {
		st += "  " + p
	}
	return st
}

func nonZero(r rune) []rune {
	if r == 0 {
		return nil
	}
	return []rune{r}
}

// Key handles a key press and reports whether the keymap consumed it.
func (s *State) Key(ev input.KeyEvent) bool {
	if s.cmdline != "" {
		return s.cmdlineKey(ev)
	}
	if s.mode == Insert {
		if ev.Key == input.KeyEscape || (ev.Key == input.KeyLeftBracket && ev.Mods.Has(input.ModCtrl)) {
			s.record(event{key: ev})
			s.leaveInsert()
			return true
		}
		s.record(event{key: ev})
		return false // the editor types
	}
	if ev.Mods.Has(input.ModSuper) || (ev.Mods.Has(input.ModAlt) && ev.Key != input.KeyEscape) {
		return false // app shortcuts
	}
	if ev.Mods.Has(input.ModCtrl) {
		switch ev.Key {
		case input.KeyR:
			s.reset()
			s.b.Redo()
			s.clampNormal()
			return true
		case input.KeyD, input.KeyU:
			n := s.takeCount(1) * 20
			if ev.Key == input.KeyU {
				n = -n
			}
			s.moveTo(s.lineMotion(s.b.Caret(), n), false)
			return true
		case input.KeyLeftBracket:
			s.escape()
			return true
		}
		return false
	}
	switch ev.Key {
	case input.KeyEscape:
		s.escape()
		return true
	case input.KeyLeft:
		return s.Text([]rune{'h'})
	case input.KeyRight:
		return s.Text([]rune{'l'})
	case input.KeyUp:
		return s.Text([]rune{'k'})
	case input.KeyDown, input.KeyEnter:
		return s.Text([]rune{'j'})
	case input.KeyBackspace:
		return s.Text([]rune{'h'})
	case input.KeyHome:
		return s.Text([]rune{'0'})
	case input.KeyEnd:
		return s.Text([]rune{'$'})
	case input.KeyDelete:
		return s.Text([]rune{'x'})
	case input.KeyTab:
		return true // no tab in normal mode
	}
	return false
}

// Text handles typed characters and reports whether the keymap consumed
// them; in insert mode the editor types them.
func (s *State) Text(runes []rune) bool {
	if s.cmdline != "" {
		s.cmdline += string(runes)
		return true
	}
	if s.mode == Insert {
		s.record(event{text: runes})
		return false
	}
	for i, r := range runes {
		switch {
		case s.cmdline != "":
			s.cmdline += string(r)
		case s.mode == Insert:
			// A command earlier in the batch started an insert: the rest is
			// typed, as the editor would have.
			rest := string(runes[i:])
			s.record(event{text: runes[i:]})
			s.typeInsert(rest)
			return true
		default:
			if s.idle() {
				s.cmdKeys = s.cmdKeys[:0] // a new command starts
			}
			s.cmdKeys = append(s.cmdKeys, event{text: []rune{r}})
			s.char(r)
		}
	}
	return true
}

// idle reports whether no command is part-typed.
func (s *State) idle() bool {
	return s.count == "" && s.opCount == "" && s.op == 0 && s.pending == "" && s.regName == 0
}

func (s *State) escape() {
	if s.mode == Visual || s.mode == VisualLine {
		s.mode = Normal
		s.b.SetSelection(s.visualCaret, s.visualCaret)
	}
	s.reset()
	s.clampNormal()
}

func (s *State) reset() {
	s.count, s.opCount, s.op, s.pending, s.regName = "", "", 0, "", 0
}

// takeCount is the typed count (times an operator's count), or def.
func (s *State) takeCount(def int) int {
	n := atoi(s.count)
	if m := atoi(s.opCount); m > 0 {
		if n == 0 {
			n = 1
		}
		n *= m
	}
	s.count, s.opCount = "", ""
	if n == 0 {
		return def
	}
	return n
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// record keeps input of the change being made, for ".".
func (s *State) record(e event) {
	if s.recording && !s.replaying {
		s.rec = append(s.rec, e)
	}
}

// startChange marks the command being typed as a change, for ".".
func (s *State) startChange() {
	s.recording = true
}

// endChange keeps the change for "." once it is complete: at once for most
// commands, when its insert ends for c, o, A and the like.
func (s *State) endChange() {
	if !s.recording || s.mode == Insert {
		return
	}
	s.recording = false
	if !s.replaying {
		if len(s.rec) == 0 {
			s.rec = append(s.rec, s.cmdKeys...)
		}
		s.last = append(s.last[:0], s.rec...)
	}
	s.rec = s.rec[:0]
}

func (s *State) repeat() {
	if len(s.last) == 0 {
		return
	}
	s.replaying = true
	events := append([]event(nil), s.last...)
	for _, e := range events {
		if e.text != nil {
			if !s.Text(e.text) && s.mode == Insert {
				s.typeInsert(string(e.text))
			}
		} else if !s.Key(e.key) && s.mode == Insert {
			s.insertKey(e.key)
		}
	}
	s.replaying = false
}

// typeInsert and insertKey stand in for the editor while "." replays an
// insert.
func (s *State) typeInsert(text string) {
	c := s.b.Caret()
	s.b.Replace(c, c, text)
}

func (s *State) insertKey(ev input.KeyEvent) {
	c := s.b.Caret()
	switch ev.Key {
	case input.KeyEnter:
		s.b.Replace(c, c, "\n")
	case input.KeyTab:
		s.b.Replace(c, c, "\t")
	case input.KeyBackspace:
		if c > 0 {
			s.b.Replace(prevRune(s.b.Bytes(), c), c, "")
		}
	}
}

func (s *State) enterInsert() {
	s.mode = Insert
	s.insertFrom = s.b.Caret()
	if s.recording {
		s.rec = append(s.rec[:0], s.cmdKeys...) // the insert's text follows
	}
	s.reset()
}

func (s *State) leaveInsert() {
	s.mode = Normal
	if c := s.b.Caret(); c > 0 && s.b.Bytes()[c-1] != '\n' {
		s.b.SetSelection(c-1, c-1) // Vim steps back onto the last character typed
	}
	s.endChange()
	s.clampNormal()
}

// clampNormal keeps the normal-mode caret on a character, not past the end
// of its line.
func (s *State) clampNormal() {
	if s.mode != Normal {
		return
	}
	b := s.b.Bytes()
	c := s.b.Caret()
	if c > 0 && (c >= len(b) || b[c] == '\n') && c > lineStart(b, c) {
		c = prevRune(b, c)
		s.b.SetSelection(c, c)
	}
}

func (s *State) cmdlineKey(ev input.KeyEvent) bool {
	switch ev.Key {
	case input.KeyEscape:
		s.cmdline = ""
	case input.KeyBackspace:
		if len(s.cmdline) <= 1 {
			s.cmdline = ""
		} else {
			s.cmdline = s.cmdline[:len(s.cmdline)-1]
		}
	case input.KeyEnter:
		line := s.cmdline
		s.cmdline = ""
		s.runCmdline(line)
	}
	return true
}

func (s *State) runCmdline(line string) {
	switch line[0] {
	case '/', '?':
		if p := line[1:]; p != "" {
			s.search = p
		}
		s.searchNext(line[0] == '?')
	case ':':
		cmd := strings.TrimSpace(line[1:])
		if n := atoi(cmd); n > 0 && strings.Trim(cmd, "0123456789") == "" {
			s.moveTo(lineStartN(s.b.Bytes(), n-1), false)
			s.moveTo(firstNonBlank(s.b.Bytes(), s.b.Caret()), false)
			return
		}
		if s.host != nil {
			s.host.Ex(cmd)
		}
	}
}
