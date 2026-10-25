package app

import (
	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/theme"

	"github.com/mirzakhany/hm/internal/workspace"
)

// Run opens the window. It must be called from the main goroutine.
func Run(opts Options) error {
	if s, err := workspace.LoadSession(); err == nil && s.Theme != "" {
		theme.Use(s.Theme)
	} else {
		theme.Use("tokyo-night")
	}
	cfg := yoga.Config{Title: "hm", Width: 1280, Height: 820, CustomTitleBar: true}
	return yoga.Run(cfg, func() *App { return Build(opts) })
}
