package app

import (
	"testing"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/input"
)

func TestZoomShortcuts(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	defer yoga.SetZoom(1)
	if yoga.Zoom() != 1 {
		t.Fatalf("zoom %v at start", yoga.Zoom())
	}
	h.press(input.KeyEqual, input.ModSuper)
	if yoga.Zoom() != 1.1 || h.app.cfg.Current().Editor.Zoom != 1.1 {
		t.Fatalf("cmd+= gave zoom %v, config %v", yoga.Zoom(), h.app.cfg.Current().Editor.Zoom)
	}
	h.press(input.KeyEqual, input.ModSuper|input.ModShift)
	if yoga.Zoom() != 1.25 {
		t.Fatalf("cmd+shift+= gave %v", yoga.Zoom())
	}
	for range 3 {
		h.press(input.KeyMinus, input.ModSuper)
	}
	if yoga.Zoom() != 0.9 {
		t.Fatalf("three zoom outs from 125%% gave %v", yoga.Zoom())
	}
	h.press(input.Key0, input.ModSuper)
	if yoga.Zoom() != 1 || h.app.cfg.Current().Editor.Zoom != 1 {
		t.Fatalf("cmd+0 gave %v", yoga.Zoom())
	}
	if ps := h.app.cfg.Problems(); len(ps) > 0 {
		t.Fatal(ps)
	}
}

func TestZoomStepsBetweenLevels(t *testing.T) {
	if i := nearestZoomStep(1.3); zoomSteps[i] != 1.25 {
		t.Fatalf("nearest to 1.3 is %v", zoomSteps[i])
	}
	if i := nearestZoomStep(0); zoomSteps[i] != 1 {
		t.Fatal("unset zoom should be 100%")
	}
}
