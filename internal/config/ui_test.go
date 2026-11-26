// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"testing"

	"github.com/yogaui/yoga/theme"
	"github.com/yogaui/yoga/ui"
)

// TestMain checks themes and shortcuts as the app does.
func TestMain(m *testing.M) {
	ThemeNames = theme.Names
	ParseKeys = func(s string) error { _, err := ui.ParseKeySeq(s); return err }
	os.Exit(m.Run())
}
