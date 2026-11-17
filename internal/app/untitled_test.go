// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/usehm/hm/internal/workspace"
)

func TestUntitledBuffer(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.newUntitled()
	h.frame()
	path := h.app.curPath()
	if !workspace.IsUntitled(path) || workspace.UntitledName(path) != "Untitled-1" {
		t.Fatalf("path %q", path)
	}
	if h.app.ws.Modified(h.app.ws.Active) {
		t.Fatal("an empty untitled buffer has nothing to save")
	}
	ed := h.app.ws.ActiveEditor()
	ed.ApplyText("package main\n")
	if !h.app.ws.Modified(h.app.ws.Active) {
		t.Fatal("an untitled buffer with text is unsaved")
	}

	// Auto save keeps the text in the scratch file; Save All refuses.
	if err := h.app.saveFile(path); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "package main\n" {
		t.Fatalf("scratch file %q", data)
	}
	if err := h.app.saveFileNow(path); !errors.Is(err, errUntitled) {
		t.Fatalf("save now: %v", err)
	}

	h.app.setUntitledLanguage(path, ".go")
	h.frame()
	goPath := h.app.curPath()
	if filepath.Ext(goPath) != ".go" || workspace.UntitledName(goPath) != "Untitled-1" || ed.Path != goPath {
		t.Fatalf("after language: %q (editor %q)", goPath, ed.Path)
	}
	if languageName(goPath) != "Go" {
		t.Fatalf("language %q", languageName(goPath))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the old scratch file should be renamed")
	}

	to, err := h.app.saveUntitledTo(goPath, filepath.Join(root, "snippet"))
	if err != nil || to != filepath.Join(root, "snippet.go") {
		t.Fatalf("save as: %q %v", to, err)
	}
	if data, _ := os.ReadFile(to); string(data) != "package main\n" {
		t.Fatalf("saved %q", data)
	}
	if h.app.curPath() != to || h.app.ws.Modified(h.app.ws.Active) {
		t.Fatalf("tab after save: %q modified=%v", h.app.curPath(), h.app.ws.Modified(h.app.ws.Active))
	}
	if _, err := os.Stat(goPath); !os.IsNotExist(err) {
		t.Fatal("the scratch file should be gone after Save As")
	}
}

func TestUntitledEmptyClosesAndIsDeleted(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.newUntitled()
	path := h.app.curPath()
	h.app.closeTab(h.app.ws.Active)
	if len(h.app.ws.Tabs) != 0 {
		t.Fatalf("tabs %+v", h.app.ws.Tabs)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("closing should delete the scratch file")
	}
	if len(h.app.ws.Closed) != 0 {
		t.Fatal("untitled buffers cannot be reopened")
	}
}

func TestUntitledSurvivesRestart(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, true)
	h.app.newUntitled()
	path := h.app.curPath()
	h.app.ws.ActiveEditor().ApplyText("keep me")
	h.app.Close()

	h2 := newHarness(t, root, true)
	ed := h2.app.bufs.Get(path)
	if ed == nil || string(ed.Bytes()) != "keep me" {
		t.Fatalf("restored %v", ed)
	}
	if !h2.app.ws.Modified(h2.app.ws.Active) {
		t.Fatal("restored text is still unsaved")
	}
}
