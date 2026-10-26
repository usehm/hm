//go:build windows

package terminal

import (
	"context"
	"os/exec"
	"strings"

	"github.com/UserExistsError/conpty"
)

var lookPath = exec.LookPath

type winProc struct{ c *conpty.ConPty }

func startProc(c Command, cols, rows int) (proc, error) {
	line := quoteArg(c.Path)
	for _, a := range c.Args {
		line += " " + quoteArg(a)
	}
	cp, err := conpty.Start(line,
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyWorkDir(c.Dir),
		conpty.ConPtyEnv(environ(c.Env)))
	if err != nil {
		return nil, err
	}
	return &winProc{c: cp}, nil
}

func quoteArg(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func (p *winProc) Read(b []byte) (int, error)  { return p.c.Read(b) }
func (p *winProc) Write(b []byte) (int, error) { return p.c.Write(b) }
func (p *winProc) Resize(cols, rows int) error { return p.c.Resize(cols, rows) }
func (p *winProc) Close() error                { return p.c.Close() }

func (p *winProc) Wait() (int, error) {
	code, err := p.c.Wait(context.Background())
	return int(code), err
}
