// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"path/filepath"
	"testing"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/theme"
)

func TestTreeIcon(t *testing.T) {
	th := theme.Current()
	for _, tc := range []struct {
		path      string
		dir, open bool
		icon      icons.Icon
	}{
		{"/p/main.go", false, false, icons.FileCode},
		{"/p/go.sum", false, false, icons.FileLock},
		{"/p/Makefile", false, false, icons.FileTerminal},
		{"/p/README.MD", false, false, icons.FileText},
		{"/p/config.yaml", false, false, icons.FileSliders},
		{"/p/unknown.zzz", false, false, icons.File},
		{"/p/.github", true, false, icons.FolderGit},
		{"/p/plain", true, false, icons.Folder},
		{"/p/plain", true, true, icons.FolderOpen},
	} {
		ic, _ := treeIcon(tc.path, tc.dir, tc.open)
		if ic.Name != tc.icon.Name {
			t.Errorf("%s: icon %s, want %s", tc.path, ic.Name, tc.icon.Name)
		}
	}
	if _, col := treeIcon("/p/main.go", false, false); col != th.Categorical[theme.HueTeal] {
		t.Error("Go files should be teal")
	}
	if _, col := treeIcon("/p/unknown.zzz", false, false); col != th.ForegroundMuted {
		t.Error("unknown files should keep the muted default")
	}
}

func TestExplorerCollapseAll(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	if !h.app.tree.Reveal(filepath.Join(root, "util", "strings.go")) {
		t.Fatal("reveal failed")
	}
	tr := h.app.tree.Tree()
	if tr.IconFor == nil {
		t.Fatal("tree should use file-type icons")
	}
	util := tr.Selected().Parent()
	if !util.Expanded() {
		t.Fatal("util should be open after reveal")
	}
	h.app.tree.CollapseAll()
	h.frame()
	if util.Expanded() {
		t.Fatal("util should be collapsed")
	}
}
