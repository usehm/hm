// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Untitled buffers are scratch files under the state folder: their text
// lives there until it is saved somewhere else, so it survives a restart.

// UntitledDir is where untitled buffers are kept.
func UntitledDir() string { return filepath.Join(StateDir(), "untitled") }

// IsUntitled reports whether path is an untitled buffer's file.
func IsUntitled(path string) bool { return filepath.Dir(path) == UntitledDir() }

// UntitledName is the buffer's name without the extension its language
// added, such as "Untitled-2".
func UntitledName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// NewUntitled creates an empty untitled buffer and opens it in a tab.
func (w *Workspace) NewUntitled() (string, error) {
	dir := UntitledDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	taken := map[string]bool{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			taken[UntitledName(e.Name())] = true
		}
	}
	for _, t := range w.Tabs {
		if IsUntitled(t.Path) {
			taken[UntitledName(t.Path)] = true
		}
	}
	name := ""
	for n := 1; ; n++ {
		if name = fmt.Sprintf("Untitled-%d", n); !taken[name] {
			break
		}
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return "", err
	}
	if _, err := w.Open(path); err != nil {
		return "", err
	}
	return path, nil
}
