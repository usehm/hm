// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Copied from chapar internal/gitx/exec_other.go (github.com/chapar-rest/chapar@2342e52a).

//go:build !windows

package gitx

import "os/exec"

func hideWindow(*exec.Cmd) {}
