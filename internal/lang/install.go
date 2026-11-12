package lang

// Adapted from chapar ui/langsrv/install.go (github.com/chapar-rest/chapar@2342e52a).

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/usehm/hm/internal/config"
)

// installTimeout bounds one install; package managers can be slow, but a
// hung one must not keep "Installing…" up forever.
const installTimeout = 10 * time.Minute

// PickInstaller returns the first installer whose tool is on PATH.
func PickInstaller(list []config.Installer) (config.Installer, bool) {
	for _, i := range list {
		if _, err := exec.LookPath(i.Tool); err == nil {
			return i, true
		}
	}
	return config.Installer{}, false
}

// InstallerString shows an installer as a command line.
func InstallerString(i config.Installer) string {
	return strings.TrimSpace(i.Tool + " " + strings.Join(i.Args, " "))
}

// Install runs inst and then checks that command is on PATH. Output lines go
// to onLine as they arrive. It blocks; run it on a goroutine.
func Install(inst config.Installer, command string, onLine func(string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, inst.Tool, inst.Args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if onLine != nil {
		onLine("$ " + InstallerString(inst))
	}
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return err
	}
	lines := make(chan struct{})
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			if line := strings.TrimRight(sc.Text(), " \r"); line != "" && onLine != nil {
				onLine(line)
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	err := cmd.Wait()
	_ = pw.Close()
	<-lines
	if ctx.Err() != nil {
		return fmt.Errorf("timed out after %v", installTimeout)
	}
	if err != nil {
		return err
	}
	// The tool may have installed into a folder this process's PATH lacks.
	FixPath()
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("installed, but %s is still not on PATH; set its full path in the config", command)
	}
	return nil
}
