// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// ScrollbackLines is how many lines scroll off the top before the oldest go.
const ScrollbackLines = 10000

// Session is one shell in a pseudo-terminal. Output is parsed into the
// emulator on a goroutine; mu guards the emulator and the fields below it,
// and the widget holds it while it paints.
type Session struct {
	p    proc
	wake func()

	mu         sync.Mutex
	emu        *vt.Emulator
	cols, rows int
	title      string
	cwd        string // reported by the shell (OSC 7)
	exited     bool
	exitCode   int
	modes      map[ansi.DECMode]bool // mouse tracking and other app-set modes
	gen        uint64                // bumps on every change to the screen
}

// Start runs cmd in a new pseudo-terminal of cols×rows. wake runs on a
// background goroutine whenever the screen changes.
func Start(cmd Command, cols, rows int, wake func()) (*Session, error) {
	s := newSession(cols, rows, wake)
	p, err := startProc(cmd, s.cols, s.rows)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	s.run(p)
	return s, nil
}

func newSession(cols, rows int, wake func()) *Session {
	cols, rows = max(cols, 2), max(rows, 1)
	s := &Session{wake: wake, cols: cols, rows: rows, modes: map[ansi.DECMode]bool{}}
	if s.wake == nil {
		s.wake = func() {}
	}
	s.emu = vt.NewEmulator(cols, rows)
	s.emu.SetScrollbackSize(ScrollbackLines)
	s.emu.SetCallbacks(vt.Callbacks{
		Title:            func(t string) { s.title = t },
		WorkingDirectory: func(d string) { s.cwd = d },
		EnableMode:       func(m ansi.Mode) { s.setMode(m, true) },
		DisableMode:      func(m ansi.Mode) { s.setMode(m, false) },
	})
	return s
}

func (s *Session) run(p proc) {
	s.p = p
	go s.pump()
	go s.readLoop()
}

// setMode runs inside emulator writes, so mu is already held.
func (s *Session) setMode(m ansi.Mode, on bool) {
	if d, ok := m.(ansi.DECMode); ok {
		s.modes[d] = on
	}
}

// pump moves input from the emulator's pipe to the PTY. Senders hold mu
// while they write the pipe, so pump never waits on the PTY itself: a
// program busy writing output (which needs mu to be parsed) could otherwise
// stall a large paste forever. A second goroutine does the PTY writes from
// an unbounded queue.
func (s *Session) pump() {
	var (
		qmu     sync.Mutex
		cond    = sync.NewCond(&qmu)
		pending [][]byte
		done    bool
	)
	go func() {
		for {
			qmu.Lock()
			for len(pending) == 0 && !done {
				cond.Wait()
			}
			if len(pending) == 0 {
				qmu.Unlock()
				return
			}
			b := pending[0]
			pending = pending[1:]
			qmu.Unlock()
			// After the process exits writes fail; the queue still drains.
			_, _ = s.p.Write(b)
		}
	}()
	buf := make([]byte, 4096)
	for {
		n, err := s.emu.Read(buf)
		qmu.Lock()
		if n > 0 {
			pending = append(pending, append([]byte(nil), buf[:n]...))
		}
		done = err != nil
		cond.Signal()
		qmu.Unlock()
		if err != nil {
			return
		}
	}
}

func (s *Session) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.p.Read(buf)
		if n > 0 {
			s.mu.Lock()
			_, _ = s.emu.Write(buf[:n])
			s.gen++
			s.mu.Unlock()
			s.wake()
		}
		if err != nil {
			break
		}
	}
	code, err := s.p.Wait()
	s.mu.Lock()
	s.exited, s.exitCode = true, code
	msg := fmt.Sprintf("\r\n\x1b[2m[process exited with code %d]\x1b[0m", code)
	if err != nil && !errors.Is(err, io.EOF) {
		msg = fmt.Sprintf("\r\n\x1b[2m[process ended: %v]\x1b[0m", err)
	}
	_, _ = s.emu.WriteString(msg)
	// End the input pipe, not the emulator: Emulator.Close flips a flag
	// that pump's Read checks without a lock.
	if pw, ok := s.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
	}
	s.gen++
	s.mu.Unlock()
	_ = s.p.Close()
	s.wake()
}

// Close ends the process (SIGHUP on Unix) and releases the PTY.
func (s *Session) Close() error {
	if s.p == nil {
		return nil
	}
	return s.p.Close()
}

// Resize changes the grid size and tells the process.
func (s *Session) Resize(cols, rows int) {
	cols, rows = max(cols, 2), max(rows, 1)
	s.mu.Lock()
	if cols == s.cols && rows == s.rows {
		s.mu.Unlock()
		return
	}
	s.cols, s.rows = cols, rows
	s.emu.Resize(cols, rows)
	s.gen++
	exited := s.exited
	s.mu.Unlock()
	if !exited {
		_ = s.p.Resize(cols, rows)
	}
}

// Size returns the grid size in cells.
func (s *Session) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// SendKey encodes a key press the way the running program expects it
// (application cursor keys, alt as escape prefix, and so on).
func (s *Session) SendKey(k vt.KeyPressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exited {
		s.emu.SendKey(k)
	}
}

// SendText types text as-is.
func (s *Session) SendText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exited {
		s.emu.SendText(text)
	}
}

// Paste sends text, bracketed when the program asked for bracketed paste.
// Newlines become carriage returns, as typed Enter keys would be.
func (s *Session) Paste(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.exited {
		s.emu.Paste(normalizePaste(text))
	}
}

func normalizePaste(text string) string {
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			out = append(out, '\r')
		case '\n':
			out = append(out, '\r')
		case 0x1b:
			// A pasted escape could end bracketed paste early; drop it.
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

// SendMouse reports a mouse event to a program that asked for them, and
// reports whether it did.
func (s *Session) SendMouse(m vt.Mouse) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited || !s.mouseTracking() {
		return false
	}
	s.emu.SendMouse(m)
	return true
}

func (s *Session) mouseTracking() bool {
	return s.modes[ansi.ModeMouseX10] || s.modes[ansi.ModeMouseNormal] ||
		s.modes[ansi.ModeMouseHighlight] || s.modes[ansi.ModeMouseButtonEvent] ||
		s.modes[ansi.ModeMouseAnyEvent]
}

// MouseTracking reports whether the running program asked for mouse events.
func (s *Session) MouseTracking() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited && s.mouseTracking()
}

// Title is the title the program set, or the shell's name.
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// Exited reports whether the process has ended, and its exit code.
func (s *Session) Exited() (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited, s.exitCode
}

// Gen changes whenever the screen does.
func (s *Session) Gen() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// ClearScrollback drops the lines above the screen.
func (s *Session) ClearScrollback() {
	s.mu.Lock()
	s.emu.ClearScrollback()
	s.gen++
	s.mu.Unlock()
}

// Text returns the screen (and with scrollback, the lines above it) as
// plain text, trailing spaces trimmed.
func (s *Session) Text(scrollback bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.grid()
	from := g.sb
	if scrollback {
		from = 0
	}
	return g.text(pos{row: from}, pos{row: g.total() - 1, col: g.cols})
}
