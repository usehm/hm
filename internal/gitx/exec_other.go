// Copied from chapar internal/gitx/exec_other.go (github.com/chapar-rest/chapar@2342e52a).

//go:build !windows

package gitx

import "os/exec"

func hideWindow(*exec.Cmd) {}
