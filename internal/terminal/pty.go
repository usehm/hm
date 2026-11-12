// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Package terminal runs shells in pseudo-terminals and draws them: a PTY per
// session, charmbracelet's VT emulator for the screen and scrollback, and a
// yoga widget that paints the cell grid and turns keys and mouse into input.
package terminal

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Command is what a session runs.
type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string // added to the inherited environment
}

// proc is a child process attached to a pseudo-terminal.
type proc interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	// Wait blocks until the process exits and returns its exit code.
	Wait() (int, error)
}

// ShellCommand resolves the configured shell: "auto" (or empty) means
// $SHELL, then a platform default. Unix shells start as login shells so the
// PATH matches a fresh terminal window.
func ShellCommand(shell, dir string) Command {
	if shell == "" || shell == "auto" {
		shell = defaultShell()
	}
	fields := strings.Fields(shell)
	cmd := Command{Path: fields[0], Args: fields[1:], Dir: dir}
	if runtime.GOOS != "windows" && len(cmd.Args) == 0 {
		switch filepath.Base(cmd.Path) {
		case "bash", "zsh", "fish", "sh", "ksh":
			cmd.Args = []string{"-l"}
		}
	}
	return cmd
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		for _, s := range []string{"pwsh.exe", "powershell.exe"} {
			if p, err := lookPath(s); err == nil {
				return p
			}
		}
		if c := os.Getenv("COMSPEC"); c != "" {
			return c
		}
		return "cmd.exe"
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	for _, s := range []string{"/bin/zsh", "/bin/bash"} {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	return "/bin/sh"
}

// environ is the child's environment: the app's, minus variables that would
// confuse a nested shell, plus the terminal's identity and cmd.Env.
func environ(extra []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "TERM", "COLORTERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLUMNS", "LINES":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=hm")
	return append(env, extra...)
}
