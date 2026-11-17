// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/usehm/hm/internal/workspace"
)

func TestOpenImageShowsIt(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 30, 20))); err != nil {
		t.Fatal(err)
	}
	logo := filepath.Join(root, "logo.png")
	if err := os.WriteFile(logo, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	h.app.openFile(logo)
	h.frame()
	if tab := h.app.ws.ActiveTab(); tab == nil || tab.Kind != workspace.KindImage {
		t.Fatalf("tab: %+v", tab)
	}
	if h.app.bufs.Get(logo) != nil {
		t.Fatal("an image should not open as text")
	}
	info := h.app.imageInfoFor(logo)
	if info.err != nil || info.format != "png" || info.w != 30 || info.h != 20 {
		t.Fatalf("info: %+v", info)
	}

	bad := filepath.Join(root, "broken.jpg")
	if err := os.WriteFile(bad, []byte("not a jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.app.openFile(bad)
	h.frame()
	if h.app.imageInfoFor(bad).err == nil {
		t.Fatal("a broken image should report an error")
	}
	h.app.closeTab(h.app.ws.Active)
	h.app.closeTab(h.app.ws.Active)
	h.frame()
	if len(h.app.ws.Tabs) != 0 {
		t.Fatalf("tabs: %+v", h.app.ws.Tabs)
	}
}
