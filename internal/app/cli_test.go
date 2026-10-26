package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAppBundle(t *testing.T) {
	for exe, want := range map[string]string{
		"/Applications/Hm.app/Contents/MacOS/Hm": "/Applications/Hm.app",
		"/usr/local/bin/hm":                      "",
		"/tmp/go-build/exe/hm":                   "",
	} {
		if got := appBundle(exe); got != want {
			t.Errorf("appBundle(%q) = %q, want %q", exe, got, want)
		}
	}
}

// The launcher hands the app an absolute folder, whatever the shell's
// working directory and whether it was given a folder or a file.
func TestCLIScriptResolvesPath(t *testing.T) {
	if !cliSupported() {
		t.Skip("no shell command on " + runtime.GOOS)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "fake-hm")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(out)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, "hm")
	if err := os.WriteFile(launcher, []byte(cliScript(fake, "", bundleID)), 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work space")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "main.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(work)

	for _, arg := range []string{".", "main.go"} {
		_ = os.Remove(out)
		cmd := exec.Command(launcher, arg)
		cmd.Dir = work
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hm %s: %v\n%s", arg, err, b)
		}
		got := waitFile(t, out)
		if strings.TrimSpace(got) != want {
			t.Errorf("hm %s passed %q, want %q", arg, got, want)
		}
	}

	if err := exec.Command(launcher, filepath.Join(dir, "missing")).Run(); err == nil {
		t.Error("hm on a missing folder succeeded")
	}
}

// waitFile reads path once the detached fake app has written it.
func waitFile(t *testing.T, path string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s was never written", path)
	return ""
}
