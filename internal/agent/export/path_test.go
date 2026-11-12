// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package export

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExportPath(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	day := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	name := "hm-fix-it-2026-11-06.html"
	if got := Path("", HTML, "Fix it", day, root, home); got != filepath.Join(home, name) {
		t.Errorf("no Downloads folder: %s", got)
	}
	if err := os.Mkdir(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ in, want string }{
		{"", filepath.Join(home, "Downloads", name)},
		{"out", filepath.Join(root, "out", name)},
		{"chat", filepath.Join(root, "chat.html")},
		{"~/x.html", filepath.Join(home, "x.html")},
		{filepath.Join(home, "abs.html"), filepath.Join(home, "abs.html")},
	}
	for _, tt := range tests {
		if got := Path(tt.in, HTML, "Fix it", day, root, home); got != tt.want {
			t.Errorf("Path(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
