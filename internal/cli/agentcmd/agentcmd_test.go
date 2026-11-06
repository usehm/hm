package agentcmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirzakhany/hm/internal/agent/agenttest"
	"github.com/mirzakhany/hm/internal/config"
)

// sum is a shell command whose output, 3345, appears nowhere in it, so a
// request containing 3345 shows the command ran.
const sum = `{"command":"echo $((1000+2345))"}`

func shellCall() agenttest.Reply {
	return agenttest.Reply{Calls: []agenttest.Call{{Name: "shell", Input: sum}}}
}

func writeCall() agenttest.Reply {
	return agenttest.Reply{Calls: []agenttest.Call{{Name: "write", Input: `{"path":"new.txt","content":"hi\n"}`}}}
}

// run runs hm agent with args in a workspace whose model follows script.
func run(t *testing.T, root string, stdin io.Reader, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Run(context.Background(), append([]string{"--cwd", root}, args...), stdin, &out, &errb)
	return code, out.String(), errb.String()
}

func TestPrint(t *testing.T) {
	cases := []struct {
		name       string
		script     []agenttest.Reply
		stdin      string
		args       []string
		code       int
		stdout     string   // exactly, when set
		stderr     string   // contained, when set
		sent       []string // in some request to the model
		notSent    []string
		file       string // new.txt's content; "-" for no file
		guardShell bool   // guard.yaml denies the shell command
	}{
		{name: "answer", script: []agenttest.Reply{{Text: "hello there"}}, args: []string{"-p", "say", "hi"},
			stdout: "hello there\n", sent: []string{"say hi"}},
		{name: "stdin joins the prompt", args: []string{"-p", "summarise"}, stdin: "line one\n",
			sent: []string{"summarise\\n\\n\\u003cstdin\\u003e\\nline one\\n\\u003c/stdin\\u003e"}},
		{name: "stdin is the prompt", args: []string{"-p"}, stdin: "just stdin", sent: []string{"just stdin"}},
		{name: "flags after the prompt", args: []string{"-p", "hi", "--model", "other-model"}, sent: []string{`"other-model"`}},
		{name: "after -- is prompt", args: []string{"-p", "--", "--model", "x"}, sent: []string{"--model x"}},
		{name: "shell refused by default", script: []agenttest.Reply{shellCall()}, args: []string{"-p", "run"},
			code: exitNeededOK, stdout: "done\n", stderr: "refused 1 call that needed approval (shell)",
			sent: []string{"rerun with --approve=all"}, notSent: []string{"3345"}},
		{name: "shell refused with edits", script: []agenttest.Reply{shellCall()}, args: []string{"-p", "--approve=edits", "run"},
			code: exitNeededOK, notSent: []string{"3345"}},
		{name: "shell allowed with all", script: []agenttest.Reply{shellCall()}, args: []string{"-p", "--approve", "all", "run"},
			sent: []string{"3345"}},
		{name: "guard deny beats all", script: []agenttest.Reply{shellCall()}, args: []string{"-p", "--approve=all", "run"},
			guardShell: true, notSent: []string{"3345"}},
		{name: "edit refused", script: []agenttest.Reply{writeCall()}, args: []string{"-p", "--mode", "careful", "write"},
			code: exitNeededOK, file: "-", sent: []string{"--approve=edits or --approve=all"}},
		{name: "edit allowed with edits", script: []agenttest.Reply{writeCall()}, args: []string{"-p", "--mode=careful", "--approve=edits", "write"},
			file: "hi\n"},
		{name: "edit allowed with all", script: []agenttest.Reply{writeCall()}, args: []string{"-p", "--mode=careful", "--approve=all", "write"},
			file: "hi\n"},
		{name: "edit in auto mode needs no approval", script: []agenttest.Reply{writeCall()}, args: []string{"-p", "write"}, file: "hi\n"},
		{name: "max steps", script: []agenttest.Reply{{Calls: []agenttest.Call{{Name: "read", Input: `{"path":"main.go"}`}}}},
			args: []string{"-p", "--max-steps", "1", "read"}, code: exitAgentError, stdout: "-", stderr: "used up its steps"},
		{name: "verbose shows calls", script: []agenttest.Reply{shellCall()}, args: []string{"-p", "-v", "--approve=all", "run"},
			stderr: "→ shell echo $((1000+2345))"},
		{name: "no -p", args: []string{"hi"}, code: exitUsage, stdout: "-", stderr: "-p is needed"},
		{name: "no prompt", args: []string{"-p"}, code: exitUsage, stdout: "-", stderr: "no prompt"},
		{name: "bad approve", args: []string{"-p", "--approve=some", "hi"}, code: exitUsage, stderr: "never, edits or all"},
		{name: "unknown mode", args: []string{"-p", "--mode", "nope", "hi"}, code: exitUsage, stderr: `no mode named "nope"`},
		{name: "unknown flag", args: []string{"-p", "--nope", "hi"}, code: exitUsage},
		{name: "session and continue", args: []string{"-p", "--session", "x", "--continue", "hi"}, code: exitUsage},
		{name: "continue without saving", args: []string{"-p", "--continue", "--no-session", "hi"}, code: exitUsage, stderr: "without saving"},
		{name: "help", args: []string{"-h"}, code: exitOK, stdout: "-", stderr: "Usage: hm agent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, model := agenttest.Workspace(t, append([]agenttest.Reply(nil), c.script...), "")
			if c.guardShell {
				agenttest.WriteFile(t, filepath.Join(config.UserDir(), "guard.yaml"), "commands:\n  deny: [\"echo *\"]\n")
			}
			var stdin io.Reader
			if c.stdin != "" {
				stdin = strings.NewReader(c.stdin)
			}
			code, stdout, stderr := run(t, root, stdin, c.args...)
			if code != c.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, c.code, stdout, stderr)
			}
			switch c.stdout {
			case "":
			case "-":
				if stdout != "" {
					t.Errorf("stdout %q, want nothing", stdout)
				}
			default:
				if stdout != c.stdout {
					t.Errorf("stdout %q, want %q", stdout, c.stdout)
				}
			}
			if !strings.Contains(stderr, c.stderr) {
				t.Errorf("stderr %q, want %q", stderr, c.stderr)
			}
			for _, s := range c.sent {
				if !model.Sent(s) {
					t.Errorf("no request contains %q", s)
				}
			}
			for _, s := range c.notSent {
				if model.Sent(s) {
					t.Errorf("a request contains %q", s)
				}
			}
			if c.file != "" {
				got, err := os.ReadFile(filepath.Join(root, "new.txt"))
				if c.file == "-" && err == nil {
					t.Errorf("new.txt was written: %q", got)
				}
				if c.file != "-" && string(got) != c.file {
					t.Errorf("new.txt %q, want %q", got, c.file)
				}
			}
		})
	}
}

func TestSessions(t *testing.T) {
	root, model := agenttest.Workspace(t, []agenttest.Reply{{Text: "noted"}, {Text: "again"}, {Text: "third"}}, "")
	sessions := filepath.Join(config.StateDir(), "sessions")
	if code, _, stderr := run(t, root, nil, "-p", "--no-session", "forget this"); code != 0 {
		t.Fatal(stderr)
	}
	if entries, _ := os.ReadDir(sessions); len(entries) != 0 {
		t.Fatalf("--no-session saved %d files", len(entries))
	}
	if code, _, stderr := run(t, root, nil, "-p", "remember the word kumquat"); code != 0 {
		t.Fatal(stderr)
	}
	code, stdout, stderr := run(t, root, nil, "-p", "--continue", "what was the word?")
	if code != 0 || stdout != "third\n" {
		t.Fatalf("exit %d %q %s", code, stdout, stderr)
	}
	// The resumed request carries the earlier turn; the unsaved one is gone.
	last := model.Last()
	if !strings.Contains(last, "kumquat") || strings.Contains(last, "forget this") {
		t.Fatalf("resumed request: %s", last)
	}
}

func TestInterrupted(t *testing.T) {
	root, _ := agenttest.Workspace(t, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb bytes.Buffer
	if code := Run(ctx, []string{"--cwd", root, "-p", "hi"}, nil, &out, &errb); code != exitInterrupted {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

// TestNoUIToolkit checks that hm agent builds without the UI toolkit, so
// it needs no display.
func TestNoUIToolkit(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/mirzakhany/hm/internal/cli/agentcmd").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "github.com/mirzakhany/yoga") {
		t.Fatal("hm agent imports the UI toolkit")
	}
}
