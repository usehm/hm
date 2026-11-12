package lang

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/usehm/hm/internal/config"
)

// formatTimeout bounds one formatter run.
const formatTimeout = 30 * time.Second

// FormatRequest is one run of a language's formatter chain.
type FormatRequest struct {
	Path    string
	Src     []byte
	Trigger string // save | manual | paste
	// Formatters is the language's chain; those whose "on" lists Trigger
	// run, in order.
	Formatters []config.Formatter
	// LSP formats with the language server. It is used when the language
	// has no formatters at all; nil when the server cannot format.
	LSP func(ctx context.Context, src []byte) ([]byte, error)
}

// FormatError is a formatter that failed; the buffer is left as it was.
type FormatError struct {
	Command string
	Stderr  string
	Err     error
}

func (e *FormatError) Error() string {
	msg := fmt.Sprintf("%s failed: %v", e.Command, e.Err)
	if e.Stderr != "" {
		msg += "\n" + lastLines(e.Stderr, 10)
	}
	return msg
}

func (e *FormatError) Unwrap() error { return e.Err }

// MissingError is a formatter that is not installed.
type MissingError struct {
	Command string
	Install []config.Installer
}

func (e *MissingError) Error() string { return e.Command + " is not installed" }

// ErrNothingToRun means no formatter is configured for the trigger.
var ErrNothingToRun = errors.New("no formatter for this file")

// Format runs the chain and returns the formatted text. On any error the
// caller keeps its buffer unchanged.
func Format(ctx context.Context, req FormatRequest) ([]byte, error) {
	var chain []config.Formatter
	for _, f := range req.Formatters {
		if slices.Contains(f.On, req.Trigger) {
			chain = append(chain, f)
		}
	}
	if len(chain) == 0 {
		if len(req.Formatters) == 0 && req.LSP != nil {
			return req.LSP(ctx, req.Src)
		}
		return nil, ErrNothingToRun
	}
	src := req.Src
	for _, f := range chain {
		command := config.ExpandHome(f.Command)
		if _, err := exec.LookPath(command); err != nil {
			return nil, &MissingError{Command: f.Command, Install: f.Install}
		}
		out, err := runFormatter(ctx, command, f, req.Path, src)
		if err != nil {
			return nil, err
		}
		src = out
	}
	return src, nil
}

func runFormatter(ctx context.Context, command string, f config.Formatter, path string, src []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, formatTimeout)
	defer cancel()
	dir := filepath.Dir(path)
	target := path
	if !f.Stdin {
		// File formatters rewrite {file} in place; give them a copy beside
		// the original, so project config applies and the real file is only
		// written by the save.
		target = filepath.Join(dir, ".hm-format-"+filepath.Base(path))
		if err := os.WriteFile(target, src, 0o600); err != nil {
			return nil, err
		}
		defer os.Remove(target)
	}
	args := make([]string, len(f.Args))
	for i, a := range f.Args {
		args[i] = strings.ReplaceAll(a, "{file}", target)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if f.Stdin {
		cmd.Stdin = bytes.NewReader(src)
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("timed out after %v", formatTimeout)
		}
		return nil, &FormatError{Command: f.Command, Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	if f.Stdin {
		if stdout.Len() == 0 && len(bytes.TrimSpace(src)) > 0 {
			return nil, &FormatError{Command: f.Command, Stderr: stderr.String(), Err: errors.New("printed nothing")}
		}
		return stdout.Bytes(), nil
	}
	return os.ReadFile(target)
}
