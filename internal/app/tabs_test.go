// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"path/filepath"
	"testing"
)

func TestCloseTabVariants(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	names := []string{"go.mod", "main.go", "util/strings.go"}
	open := func() {
		h.app.ws.CloseAll()
		for _, n := range names {
			h.app.openFile(filepath.Join(root, n))
		}
		h.frame()
	}
	tabNames := func() []string {
		var out []string
		for _, tb := range h.app.ws.Tabs {
			out = append(out, h.app.ws.Rel(tb.Path))
		}
		return out
	}
	check := func(what string, want ...string) {
		t.Helper()
		got := tabNames()
		if len(got) != len(want) {
			t.Fatalf("%s: tabs %v, want %v", what, got, want)
		}
		for i := range want {
			if filepath.ToSlash(got[i]) != want[i] {
				t.Fatalf("%s: tabs %v, want %v", what, got, want)
			}
		}
	}

	open()
	h.app.closeRight(0)
	check("close right", "go.mod")

	open()
	h.app.ws.Active = 1
	h.app.closeOthers(1)
	check("close others", "main.go")
	if h.app.ws.Active != 0 {
		t.Fatalf("active %d, want the kept tab", h.app.ws.Active)
	}

	open()
	h.app.ws.Active = 2
	h.app.bufs.Get(filepath.Join(root, "main.go")).SetText("changed")
	h.app.closeSaved()
	check("close saved", "main.go")
	if h.c.Dialogs().Open {
		t.Fatal("close saved must not ask")
	}

	h.app.closeAllTabs()
	if !h.c.Dialogs().Open {
		t.Fatal("closing a dirty tab must ask")
	}
	check("close all asks first", "main.go")
}
