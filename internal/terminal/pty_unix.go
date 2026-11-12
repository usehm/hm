// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

var lookPath = exec.LookPath

type unixProc struct {
	f   *os.File
	cmd *exec.Cmd
}

func startProc(c Command, cols, rows int) (proc, error) {
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = environ(c.Env)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixProc{f: f, cmd: cmd}, nil
}

func (p *unixProc) Read(b []byte) (int, error) {
	n, err := p.f.Read(b)
	// Linux reports EIO once the child side closes; that is a normal end.
	if errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
		err = io.EOF
	}
	return n, err
}

func (p *unixProc) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *unixProc) Resize(cols, rows int) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *unixProc) Wait() (int, error) {
	err := p.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (p *unixProc) Close() error {
	if p.cmd.Process != nil {
		// SIGHUP, as when a terminal window closes; the shell passes it on.
		_ = p.cmd.Process.Signal(syscall.SIGHUP)
	}
	return p.f.Close()
}
