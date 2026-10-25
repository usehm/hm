package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

const (
	defaultShellTimeout = 2 * time.Minute
	maxShellTimeout     = 30 * time.Minute
	shellOutputMax      = 30000
)

// IsDestructiveGit reports whether a shell command runs a git operation
// that loses work or rewrites shared history. Those always need approval,
// whatever the mode says.
func IsDestructiveGit(cmd string) bool {
	for _, seg := range splitCommands(cmd) {
		f := strings.Fields(seg)
		i := 0
		for i < len(f) && f[i] != "git" {
			i++
		}
		if i == len(f) {
			continue
		}
		i++
		// Skip global options, and the value of those that take one.
		for i < len(f) && strings.HasPrefix(f[i], "-") {
			switch f[i] {
			case "-C", "-c", "--git-dir", "--work-tree", "--namespace":
				i++
			}
			i++
		}
		if i >= len(f) {
			continue
		}
		sub, rest := f[i], f[i+1:]
		has := func(flags ...string) bool {
			for _, r := range rest {
				for _, fl := range flags {
					if r == fl || (len(fl) == 2 && fl[0] == '-' && strings.HasPrefix(r, "-") && !strings.HasPrefix(r, "--") && strings.ContainsRune(r[1:], rune(fl[1]))) {
						return true
					}
				}
			}
			return false
		}
		switch sub {
		case "push", "rebase", "filter-branch", "restore":
			return true
		case "reset":
			if has("--hard", "--merge", "--keep") {
				return true
			}
		case "clean":
			if has("-f", "--force") {
				return true
			}
		case "branch":
			if has("-d", "-D", "--delete") {
				return true
			}
		case "checkout":
			if has("--", "-f", "--force") {
				return true
			}
		case "reflog":
			if len(rest) > 0 && rest[0] == "expire" {
				return true
			}
		case "update-ref":
			if has("-d") {
				return true
			}
		case "stash":
			if len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear") {
				return true
			}
		}
	}
	return false
}

// splitCommands cuts a command line at ; && || | and newlines.
func splitCommands(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '&' || r == '|' || r == '\n' || r == '(' || r == ')' })
}

// shellCommand runs a command line with the user's shell semantics.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", line)
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return exec.CommandContext(ctx, sh, "-c", line)
}

// --- shell ------------------------------------------------------------------

type shellTool struct{ shells *Shells }

func (*shellTool) Spec() provider.Tool {
	return provider.Tool{Name: "shell", Description: "Run a shell command in the workspace root and return its " +
		"output and exit code. Set background for servers and watchers, then read them with shell_output.",
		Schema: schema(map[string]any{
			"command":         str("The command line"),
			"timeout_seconds": integer("Kill the command after this many seconds (default 120, max 1800)"),
			"background":      boolean("Start it and return at once with an id"),
			"cwd":             str("Folder to run in, relative to the workspace root"),
		}, "command")}
}

func (t *shellTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Background     bool   `json:"background"`
		Cwd            string `json:"cwd"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(in.Command) == "" {
		return Plan{}, errors.New("command is empty")
	}
	dir := resolve(env, in.Cwd)
	cat := CatShell
	if IsDestructiveGit(in.Command) {
		cat = CatGitPush
	}
	timeout := defaultShellTimeout
	if in.TimeoutSeconds > 0 {
		timeout = min(time.Duration(in.TimeoutSeconds)*time.Second, maxShellTimeout)
	}
	summary := in.Command
	if in.Background {
		summary += "   (background)"
	}
	return Plan{Category: cat, Summary: summary, Paths: []string{dir}, GrantKey: "shell:" + firstWord(in.Command),
		Run: func(ctx context.Context) Result {
			if in.Background {
				id, err := t.shells.Start(in.Command, dir)
				if err != nil {
					return fail("%v", err)
				}
				return ok("Started in the background as %s. Read its output with shell_output.", id)
			}
			return runCommand(ctx, in.Command, dir, timeout)
		}}, nil
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// runCommand runs line in dir and reports output and exit status.
func runCommand(ctx context.Context, line, dir string, timeout time.Duration) Result {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := shellCommand(ctx, line)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := clip(out.String(), shellOutputMax)
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return fail("%s\n[timed out after %v]", text, timeout)
	case err != nil:
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			return fail("%s\n[exit status %d]", text, xe.ExitCode())
		}
		return fail("%v", err)
	}
	if text == "" {
		text = "(no output)"
	}
	return ok("%s\n[exit status 0]", text)
}

// Shells tracks background commands.
type Shells struct {
	mu   sync.Mutex
	next int
	jobs map[string]*job
}

type job struct {
	cmd  *exec.Cmd
	out  *lockedBuffer
	read int
	done chan struct{}
	err  error
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// NewShells returns an empty job table.
func NewShells() *Shells { return &Shells{jobs: map[string]*job{}} }

// Start runs line in the background.
func (s *Shells) Start(line, dir string) (string, error) {
	cmd := shellCommand(context.Background(), line)
	cmd.Dir = dir
	j := &job{cmd: cmd, out: &lockedBuffer{}, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = j.out, j.out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { j.err = cmd.Wait(); close(j.done) }()
	s.mu.Lock()
	s.next++
	id := fmt.Sprintf("bg%d", s.next)
	s.jobs[id] = j
	s.mu.Unlock()
	return id, nil
}

// Output returns what a job printed since the last read, and its state.
func (s *Shells) Output(id string, kill bool) (string, error) {
	s.mu.Lock()
	j := s.jobs[id]
	s.mu.Unlock()
	if j == nil {
		return "", fmt.Errorf("no background command %s", id)
	}
	if kill && j.cmd.Process != nil {
		_ = j.cmd.Process.Kill()
		<-j.done
	}
	all := j.out.String()
	fresh := all[j.read:]
	j.read = len(all)
	state := "running"
	select {
	case <-j.done:
		state = "exited"
		var xe *exec.ExitError
		if errors.As(j.err, &xe) {
			state = fmt.Sprintf("exited with status %d", xe.ExitCode())
		}
	default:
	}
	return fmt.Sprintf("%s\n[%s]", clip(fresh, shellOutputMax), state), nil
}

// StopAll kills every background command, when the session ends.
func (s *Shells) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.cmd.Process != nil {
			_ = j.cmd.Process.Kill()
		}
	}
}

type shellOutputTool struct{ shells *Shells }

func (*shellOutputTool) Spec() provider.Tool {
	return provider.Tool{Name: "shell_output", Description: "Read new output from a background command, or stop it.",
		Schema: schema(map[string]any{
			"id":   str("The id shell returned"),
			"stop": boolean("Kill the command"),
		}, "id")}
}

func (t *shellOutputTool) Prepare(_ Env, input json.RawMessage) (Plan, error) {
	var in struct {
		ID   string `json:"id"`
		Stop bool   `json:"stop"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	return Plan{Category: CatRead, Summary: "Output of " + in.ID, Run: func(context.Context) Result {
		out, err := t.shells.Output(in.ID, in.Stop)
		if err != nil {
			return fail("%v", err)
		}
		return ok("%s", out)
	}}, nil
}

// --- git (read-only) ----------------------------------------------------------

type gitTool struct{ name string }

func (g gitTool) Spec() provider.Tool {
	switch g.name {
	case "git_diff":
		return provider.Tool{Name: g.name, Description: "Show uncommitted changes as a unified diff.",
			Schema: schema(map[string]any{
				"path":   str("Limit to this file or folder"),
				"staged": boolean("Show staged changes instead of unstaged"),
			})}
	case "git_log":
		return provider.Tool{Name: g.name, Description: "Show recent commits, newest first.",
			Schema: schema(map[string]any{
				"count": integer("How many commits (default 20)"),
				"path":  str("Only commits touching this path"),
			})}
	}
	return provider.Tool{Name: g.name, Description: "Show the branch and changed files.", Schema: schema(map[string]any{})}
}

func (g gitTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Path   string `json:"path"`
		Staged bool   `json:"staged"`
		Count  int    `json:"count"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	var args []string
	switch g.name {
	case "git_diff":
		args = []string{"diff", "--no-color"}
		if in.Staged {
			args = append(args, "--cached")
		}
	case "git_log":
		n := in.Count
		if n <= 0 {
			n = 20
		}
		args = []string{"log", "--no-color", fmt.Sprintf("-%d", n), "--format=%h %ad %an: %s", "--date=short"}
	default:
		args = []string{"status", "--short", "--branch"}
	}
	if in.Path != "" {
		args = append(args, "--", resolve(env, in.Path))
	}
	return Plan{Category: CatRead, Summary: "git " + strings.Join(args, " "), Run: func(ctx context.Context) Result {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = env.Root()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fail("%s", strings.TrimSpace(string(out)))
		}
		if len(bytes.TrimSpace(out)) == 0 {
			return ok("(nothing)")
		}
		return ok("%s", clip(string(out), 40000))
	}}, nil
}
