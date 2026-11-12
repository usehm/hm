// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyInto(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package a\n")
	write(t, filepath.Join(root, "sub", "b.go"), "package b\n")
	write(t, filepath.Join(root, "dst", "keep"), "")

	got, err := CopyInto(filepath.Join(root, "a.go"), filepath.Join(root, "dst"))
	if err != nil || got != filepath.Join(root, "dst", "a.go") {
		t.Fatalf("copy into another folder: %q %v", got, err)
	}
	// Beside the original, the copy gets a new name each time.
	for _, want := range []string{"a copy.go", "a copy 2.go"} {
		got, err := CopyInto(filepath.Join(root, "a.go"), root)
		if err != nil || got != filepath.Join(root, want) {
			t.Fatalf("copy beside the original: %q %v, want %s", got, err, want)
		}
	}
	got, err = CopyInto(filepath.Join(root, "sub"), filepath.Join(root, "dst"))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(got, "b.go")); err != nil || string(b) != "package b\n" {
		t.Fatalf("folder copy: %q %v", b, err)
	}
	if _, err := CopyInto(filepath.Join(root, "sub"), filepath.Join(root, "sub")); err == nil {
		t.Fatal("copying a folder into itself should fail")
	}
}

func TestMoveInto(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.go"), "package a\n")
	write(t, filepath.Join(root, "sub", "a.go"), "")

	got, err := MoveInto(filepath.Join(root, "a.go"), root)
	if err != nil || got != filepath.Join(root, "a.go") {
		t.Fatalf("move into its own folder should do nothing: %q %v", got, err)
	}
	got, err = MoveInto(filepath.Join(root, "a.go"), filepath.Join(root, "sub"))
	if err != nil || got != filepath.Join(root, "sub", "a 2.go") {
		t.Fatalf("move onto a taken name: %q %v", got, err)
	}
	if _, err := MoveInto(filepath.Join(root, "sub"), filepath.Join(root, "sub", "x")); err == nil {
		t.Fatal("moving a folder into itself should fail")
	}
}
