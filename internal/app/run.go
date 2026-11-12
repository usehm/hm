// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"log"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/theme"

	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/update"
)

// Run opens the window. It must be called from the main goroutine.
func Run(opts Options) error {
	// Pick the theme before the window exists so the first frame matches;
	// Build applies the rest of the config, project file included.
	user, _ := config.ReadLayer(config.UserPath())
	cfg, _ := config.Load(user)
	theme.Use(cfg.Editor.Theme)
	win := yoga.Config{Title: "hm", Width: 1280, Height: 820, CustomTitleBar: true}
	if err := yoga.Run(win, func() *App { return Build(opts) }); err != nil {
		return err
	}
	if restartAfterExit.Load() {
		if err := update.Restart(); err != nil {
			log.Printf("restarting after the update: %v", err)
		}
	}
	return nil
}
