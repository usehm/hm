// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"math"
	"strconv"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/ui"
)

// zoomSteps are the levels zoom in and out move between, as browsers do.
var zoomSteps = []float64{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}

// applyZoom pushes editor.zoom into the window; 0 (unset) is 100%.
func applyZoom(z float64) {
	if z <= 0 {
		z = 1
	}
	yoga.SetZoom(float32(z))
}

// zoomStep moves the zoom dir steps (±1) from the current level and saves
// it to the user config.
func (a *App) zoomStep(dir int) {
	cur := a.cfg.Current().Editor.Zoom
	if cur <= 0 {
		cur = 1
	}
	i := nearestZoomStep(cur)
	switch {
	case dir > 0 && zoomSteps[i] <= cur:
		i++
	case dir < 0 && zoomSteps[i] >= cur:
		i--
	}
	a.setZoom(zoomSteps[min(max(i, 0), len(zoomSteps)-1)])
}

func (a *App) setZoom(z float64) {
	applyZoom(z) // now, not after the config file round trip
	a.setUserSetting([]string{"editor", "zoom"}, z)
}

func nearestZoomStep(z float64) int {
	if z <= 0 {
		z = 1
	}
	best := 0
	for i, s := range zoomSteps {
		if math.Abs(s-z) < math.Abs(zoomSteps[best]-z) {
			best = i
		}
	}
	return best
}

func zoomOptions() []ui.SelectOption {
	opts := make([]ui.SelectOption, len(zoomSteps))
	for i, s := range zoomSteps {
		v := strconv.FormatFloat(s, 'g', -1, 64)
		opts[i] = ui.SelectOption{Label: strconv.Itoa(int(math.Round(s*100))) + "%", Value: v}
	}
	return opts
}
