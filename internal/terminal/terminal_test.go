package terminal

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
)

// fakeProc is a process whose output the test writes and whose input the
// test reads.
type fakeProc struct {
	out  *io.PipeReader
	outW *io.PipeWriter
	mu   sync.Mutex
	in   bytes.Buffer
	size [2]int
	done chan struct{}
}

func newFake() *fakeProc {
	r, w := io.Pipe()
	return &fakeProc{out: r, outW: w, done: make(chan struct{})}
}

func (f *fakeProc) Read(b []byte) (int, error) { return f.out.Read(b) }
func (f *fakeProc) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.in.Write(b)
}
func (f *fakeProc) Resize(c, r int) error { f.size = [2]int{c, r}; return nil }
func (f *fakeProc) Wait() (int, error)    { <-f.done; return 0, nil }
func (f *fakeProc) Close() error {
	select {
	case <-f.done:
	default:
		close(f.done)
	}
	return f.outW.Close()
}

func (f *fakeProc) input() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.in.String()
}

func fakeSession(t *testing.T, cols, rows int) (*Session, *fakeProc) {
	t.Helper()
	s := newSession(cols, rows, nil)
	f := newFake()
	s.run(f)
	t.Cleanup(func() { s.Close() })
	return s, f
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOutputScrollbackAndText(t *testing.T) {
	s, f := fakeSession(t, 20, 4)
	for i := range 10 {
		io.WriteString(f.outW, "line "+string(rune('0'+i))+"\r\n")
	}
	io.WriteString(f.outW, "\x1b[31mred\x1b[0m$ ")
	eventually(t, "output parsed", func() bool { return strings.Contains(s.Text(false), "red$") })
	all := s.Text(true)
	if !strings.HasPrefix(all, "line 0\nline 1\n") || !strings.HasSuffix(all, "line 9\nred$") {
		t.Fatalf("text with scrollback:\n%s", all)
	}
	s.mu.Lock()
	g := s.grid()
	c := g.cell(g.sb+3, 0)
	s.mu.Unlock()
	if c == nil || c.Content != "r" || c.Style.Fg == nil {
		t.Fatalf("red cell = %+v", c)
	}
}

func TestKeysEncodeForTheProgram(t *testing.T) {
	s, f := fakeSession(t, 20, 4)
	for _, ev := range []input.KeyEvent{
		{Key: input.KeyC, Mods: input.ModCtrl},
		{Key: input.KeyUp},
		{Key: input.KeyLeft, Mods: input.ModCtrl},
		{Key: input.KeyPageDown},
		{Key: input.KeyEnter},
		{Key: input.KeyTab, Mods: input.ModShift},
	} {
		if !s.sendKeyEvent(ev) {
			t.Fatalf("%v not sent", ev)
		}
	}
	if s.sendKeyEvent(input.KeyEvent{Key: input.KeyA}) {
		t.Fatal("a plain letter must come through text input")
	}
	want := "\x03\x1b[A\x1b[1;5D\x1b[6~\r\x1b[Z"
	eventually(t, "keys", func() bool { return f.input() == want })

	// Application cursor keys switch the arrows to SS3.
	io.WriteString(f.outW, "\x1b[?1h")
	eventually(t, "mode", func() bool { return s.Gen() > 0 })
	s.sendKeyEvent(input.KeyEvent{Key: input.KeyUp})
	eventually(t, "app cursor", func() bool { return strings.HasSuffix(f.input(), "\x1bOA") })
}

func TestBracketedPaste(t *testing.T) {
	s, f := fakeSession(t, 20, 4)
	s.Paste("a\nb")
	eventually(t, "plain paste", func() bool { return f.input() == "a\rb" })
	io.WriteString(f.outW, "\x1b[?2004h")
	eventually(t, "mode", func() bool { return s.Gen() > 0 })
	s.Paste("c\r\nd\x1b")
	eventually(t, "bracketed paste", func() bool { return f.input() == "a\rb\x1b[200~c\rd\x1b[201~" })
}

func TestMouseReportingOnlyWhenAsked(t *testing.T) {
	s, f := fakeSession(t, 20, 4)
	if s.MouseTracking() {
		t.Fatal("tracking before the program asked")
	}
	io.WriteString(f.outW, "\x1b[?1000h\x1b[?1006h")
	eventually(t, "tracking", s.MouseTracking)
}

func TestResizeReachesTheProcess(t *testing.T) {
	s, f := fakeSession(t, 20, 4)
	s.Resize(40, 10)
	if c, r := s.Size(); c != 40 || r != 10 || f.size != [2]int{40, 10} {
		t.Fatalf("size = %d×%d, process got %v", c, r, f.size)
	}
}

func TestExitIsShown(t *testing.T) {
	s, f := fakeSession(t, 40, 4)
	f.Close()
	eventually(t, "exit", func() bool { ok, _ := s.Exited(); return ok })
	if !strings.Contains(s.Text(false), "process exited with code 0") {
		t.Fatalf("screen: %q", s.Text(false))
	}
	s.SendText("ignored") // must not block or panic after exit
}

func TestClaims(t *testing.T) {
	if !claims(input.KeyEvent{Key: input.KeyP, Mods: input.ModCtrl}) {
		t.Fatal("ctrl+p must reach the shell")
	}
	if claims(input.KeyEvent{Key: input.KeyP, Mods: input.ModSuper}) {
		t.Fatal("cmd+p belongs to the app")
	}
	if claims(input.KeyEvent{Key: input.KeyTab, Mods: input.ModCtrl}) {
		t.Fatal("ctrl+tab moves focus")
	}
	shiftCtrl := claims(input.KeyEvent{Key: input.KeyC, Mods: input.ModCtrl | input.ModShift})
	if shiftCtrl != (runtime.GOOS == "darwin") {
		t.Fatalf("ctrl+shift+c claimed = %v on %s", shiftCtrl, runtime.GOOS)
	}
}

func TestShellRunsInPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell")
	}
	dir := t.TempDir()
	s, err := Start(Command{Path: "/bin/sh", Args: []string{"-c", `stty size; printf 'tty:'; read x; echo "got:$x"; stty size`}, Dir: dir}, 30, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eventually(t, "prompt", func() bool { return strings.Contains(s.Text(false), "tty:") })
	if !strings.Contains(s.Text(false), "5 30") {
		t.Fatalf("stty size before resize: %q", s.Text(false))
	}
	s.Resize(50, 8)
	s.SendText("hello\r")
	eventually(t, "echo", func() bool { return strings.Contains(s.Text(false), "got:hello") })
	eventually(t, "resized", func() bool { return strings.Contains(s.Text(false), "8 50") })
	eventually(t, "exit", func() bool { ok, _ := s.Exited(); return ok })
}

func TestShellCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shells")
	}
	c := ShellCommand("/bin/zsh", "/tmp")
	if c.Path != "/bin/zsh" || len(c.Args) != 1 || c.Args[0] != "-l" || c.Dir != "/tmp" {
		t.Fatalf("zsh = %+v", c)
	}
	c = ShellCommand("/usr/bin/fish --private", "")
	if c.Path != "/usr/bin/fish" || strings.Join(c.Args, " ") != "--private" {
		t.Fatalf("fish = %+v", c)
	}
	t.Setenv("SHELL", "/bin/bash")
	if c = ShellCommand("auto", ""); c.Path != "/bin/bash" {
		t.Fatalf("auto = %+v", c)
	}
}

func TestSelectionText(t *testing.T) {
	s, f := fakeSession(t, 20, 3)
	io.WriteString(f.outW, "foo bar.baz\r\nsecond")
	eventually(t, "output", func() bool { return strings.Contains(s.Text(false), "second") })
	s.mu.Lock()
	g := s.grid()
	a, b := g.wordAt(pos{g.sb, 5})
	word := g.text(a, b)
	two := g.text(pos{g.sb, 4}, pos{g.sb + 1, 3})
	s.mu.Unlock()
	if word != "bar.baz" {
		t.Fatalf("word = %q", word)
	}
	if two != "bar.baz\nsec" {
		t.Fatalf("two lines = %q", two)
	}
}
