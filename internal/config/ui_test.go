package config

import (
	"os"
	"testing"

	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"
)

// TestMain checks themes and shortcuts as the app does.
func TestMain(m *testing.M) {
	ThemeNames = theme.Names
	ParseKeys = func(s string) error { _, err := ui.ParseKeySeq(s); return err }
	os.Exit(m.Run())
}
