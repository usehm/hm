// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Copied from chapar internal/update/install_other.go (github.com/chapar-rest/chapar@2342e52a).

//go:build !darwin && !windows

package update

import "context"

// Detect: Linux installs put the binary under /usr/local/bin with sudo, so
// the app can't replace itself; the user downloads the new version.
func Detect() Method { return Manual }

func Install(context.Context, string) error { return ErrUnsupported }

func Restart() error { return ErrUnsupported }

func Cleanup() {}
