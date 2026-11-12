// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Copied from chapar internal/gitx/exec_windows.go (github.com/chapar-rest/chapar@2342e52a).

package gitx

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps git from opening a console window: hm is a GUI
// app, so a console program it starts would get a window of its own.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
