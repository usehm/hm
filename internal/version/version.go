// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Package version names this build of hm.
package version

// Version is set at build time with -ldflags "-X .../version.Version=v1.2.3".
// Development builds keep "dev", which never asks to update.
var Version = "dev"

// Agent is the User-Agent hm sends.
func Agent() string { return "hm/" + Version }
