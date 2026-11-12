package app

import (
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/config"
)

// The config checks theme names and shortcuts against the UI toolkit, which
// the config package itself does not import.
func init() {
	config.ThemeNames = theme.Names
	config.ParseKeys = func(s string) error { _, err := ui.ParseKeySeq(s); return err }
}
