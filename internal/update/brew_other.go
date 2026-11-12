// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Copied from chapar internal/update/brew_other.go (github.com/chapar-rest/chapar@2342e52a).

//go:build !darwin

package update

import (
	"context"
	"io"
)

// InstalledVersion is only known for macOS app bundles.
func InstalledVersion() string { return "" }

func BrewUpgrade(context.Context, io.Writer) error { return ErrUnsupported }
