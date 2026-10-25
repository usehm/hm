package app

import (
	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/theme"

	"github.com/mirzakhany/hm/internal/config"
)

// Run opens the window. It must be called from the main goroutine.
func Run(opts Options) error {
	// Pick the theme before the window exists so the first frame matches;
	// Build applies the rest of the config, project file included.
	user, _ := config.ReadLayer(config.UserPath())
	cfg, _ := config.Load(user)
	theme.Use(cfg.Editor.Theme)
	win := yoga.Config{Title: "hm", Width: 1280, Height: 820, CustomTitleBar: true}
	return yoga.Run(win, func() *App { return Build(opts) })
}
