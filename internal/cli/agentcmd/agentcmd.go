// Package agentcmd is `hm agent`: hm's coding agent on the command line,
// without the window. It needs no display, and does not import the UI
// toolkit.
package agentcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/mirzakhany/hm/pkg/hm/agent"
)

// Exit codes.
const (
	exitOK          = 0
	exitAgentError  = 1   // the model call failed, a key is missing, the turn used up its steps
	exitUsage       = 2   // bad flags, no prompt, unknown mode, provider or session
	exitNeededOK    = 3   // a call was refused because nobody could approve it
	exitInterrupted = 130 // Ctrl-C
)

const usage = `Usage: hm agent -p [flags] [prompt...]

Runs hm's agent on the prompt in the workspace and prints its final
answer. Piped standard input is added to the prompt; without a prompt it
is the prompt. Flags may come before or after the prompt.

Flags:
`

// Main runs `hm agent` with the process's arguments after "agent" and
// returns the exit code. Ctrl-C stops the turn; the session is saved.
func Main(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, args, os.Stdin, os.Stdout, os.Stderr)
}

// options are the parsed flags.
type options struct {
	print                    bool
	mode, provider, model    string
	session, cwd, approve    string
	cont, noSession, verbose bool
	maxSteps                 int
	words                    []string
}

// parse reads flags, which may come before, between or after the words
// of the prompt; everything after "--" is prompt.
func parse(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("hm agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage); fs.PrintDefaults() }
	fs.BoolVar(&o.print, "p", false, "print mode: run the prompt, print the final answer and exit")
	fs.StringVar(&o.mode, "mode", "", "agent mode, such as lazy or auto (default agent.default_mode)")
	fs.StringVar(&o.provider, "provider", "", "provider name under agent.providers (default agent.default_provider)")
	fs.StringVar(&o.model, "model", "", "model id (default the provider's model)")
	fs.StringVar(&o.session, "session", "", "resume the saved session with this id")
	fs.BoolVar(&o.cont, "continue", false, "resume the newest session of the workspace")
	fs.BoolVar(&o.noSession, "no-session", false, "do not save the session")
	fs.StringVar(&o.cwd, "cwd", "", "workspace folder (default the current folder)")
	fs.IntVar(&o.maxSteps, "max-steps", 0, "model calls per turn (default 80)")
	fs.StringVar(&o.approve, "approve", approveNever, "calls needing approval: never, edits (file edits only) or all")
	fs.BoolVar(&o.verbose, "v", false, "show tool calls and retries on standard error")

	rest, tail := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		rest, tail = args[:i], args[i+1:]
	}
	for {
		if err := fs.Parse(rest); err != nil {
			return o, err
		}
		if fs.NArg() == 0 {
			break
		}
		o.words = append(o.words, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	o.words = append(o.words, tail...)
	switch {
	case !o.print:
		return o, errors.New("-p is needed for now: hm agent -p \"your prompt\"")
	case o.approve != approveNever && o.approve != approveEdits && o.approve != approveAll:
		return o, fmt.Errorf("--approve must be never, edits or all, not %q", o.approve)
	case o.maxSteps < 0:
		return o, errors.New("--max-steps must be positive")
	case o.session != "" && o.cont:
		return o, errors.New("use --session or --continue, not both")
	}
	return o, nil
}

// prompt is the words of the prompt and piped standard input.
func prompt(words []string, stdin io.Reader) (string, error) {
	text := strings.TrimSpace(strings.Join(words, " "))
	if !piped(stdin) {
		return text, nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("reading standard input: %w", err)
	}
	in := strings.TrimSpace(string(data))
	switch {
	case in == "":
		return text, nil
	case text == "":
		return in, nil
	}
	return text + "\n\n<stdin>\n" + in + "\n</stdin>", nil
}

// piped reports whether stdin has input to read: a pipe or file rather
// than a terminal. A reader that is not a file, as in tests, counts.
func piped(stdin io.Reader) bool {
	if stdin == nil {
		return false
	}
	f, ok := stdin.(*os.File)
	if !ok {
		return true
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice == 0
}

// Run is Main with its context and streams given.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o, err := parse(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		fmt.Fprintln(stderr, "hm agent:", err)
		return exitUsage
	}
	text, err := prompt(o.words, stdin)
	if err != nil {
		fmt.Fprintln(stderr, "hm agent:", err)
		return exitUsage
	}
	if text == "" {
		fmt.Fprintln(stderr, "hm agent: no prompt; give one after -p or pipe it in")
		return exitUsage
	}
	pol := &policy{mode: o.approve}
	r, err := agent.New(agent.Options{
		Root: o.cwd, Mode: o.mode, Provider: o.provider, Model: o.model,
		SessionID: o.session, Continue: o.cont, NoSession: o.noSession, MaxSteps: o.maxSteps,
		Approver: pol, Logger: slog.New(&lineHandler{w: stderr, level: slog.LevelWarn}),
	})
	if err != nil {
		fmt.Fprintln(stderr, "hm agent:", err)
		return exitUsage
	}
	var shown chan struct{}
	if o.verbose {
		shown = make(chan struct{})
		go func() { defer close(shown); show(r.Events(), stderr) }()
	}
	err = r.Prompt(ctx, text)
	_ = r.Close()
	if shown != nil {
		<-shown
	}
	switch {
	case err != nil && ctx.Err() != nil:
		fmt.Fprintln(stderr, "hm agent: interrupted")
		return exitInterrupted
	case err != nil:
		fmt.Fprintln(stderr, "hm agent:", err)
		return exitAgentError
	}
	if answer := r.Answer(); answer != "" {
		fmt.Fprintln(stdout, answer)
	}
	if refused := pol.refusals(); len(refused) > 0 {
		fmt.Fprintf(stderr, "hm agent: refused %d %s that needed approval (%s); rerun with --approve to allow them\n",
			len(refused), plural(len(refused), "call"), strings.Join(slices.Compact(slices.Sorted(slices.Values(refused))), ", "))
		return exitNeededOK
	}
	return exitOK
}

// show writes what the agent does to w, a line each, for -v.
func show(events <-chan agent.Event, w io.Writer) {
	for e := range events {
		switch e.Kind {
		case agent.EventToolCall:
			fmt.Fprintf(w, "→ %s %s\n", e.Tool, firstLine(e.Summary))
		case agent.EventToolResult:
			if e.Result.IsError {
				fmt.Fprintf(w, "✗ %s: %s\n", e.Tool, firstLine(e.Result.Text))
			}
		case agent.EventBlocked:
			fmt.Fprintf(w, "✗ guard blocked %s: %s\n", e.Tool, e.Text)
		case agent.EventRetry, agent.EventError:
			fmt.Fprintf(w, "! %s\n", e.Text)
		case agent.EventCompacted:
			if e.Text != "" {
				fmt.Fprintf(w, "· %s\n", e.Text)
			}
		}
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// lineHandler writes log records as "hm agent: message" lines.
type lineHandler struct {
	w     io.Writer
	level slog.Level
}

func (h *lineHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *lineHandler) Handle(_ context.Context, r slog.Record) error {
	prefix := "hm agent: "
	if r.Level == slog.LevelWarn {
		prefix += "warning: "
	}
	_, err := fmt.Fprintln(h.w, prefix+r.Message)
	return err
}

func (h *lineHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *lineHandler) WithGroup(string) slog.Handler      { return h }
