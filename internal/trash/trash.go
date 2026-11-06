// Package trash moves files to the user's trash, so deleting them can be
// undone.
package trash

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Move moves path to the user's trash so a delete can be undone.
func Move(path string) error {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		return os.Rename(path, FreeName(filepath.Join(home, ".Trash"), filepath.Base(path)))
	case "windows":
		ps := "Add-Type -AssemblyName Microsoft.VisualBasic; " +
			"$p = $args[0]; if (Test-Path -PathType Container $p) " +
			"{ [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteDirectory($p,'OnlyErrorDialogs','SendToRecycleBin') } " +
			"else { [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile($p,'OnlyErrorDialogs','SendToRecycleBin') }"
		return exec.Command("powershell", "-NoProfile", "-Command", ps, path).Run()
	}
	return trashXDG(path)
}

// trashXDG follows the freedesktop.org trash spec.
func trashXDG(path string) error {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		data = filepath.Join(home, ".local", "share")
	}
	files, info := filepath.Join(data, "Trash", "files"), filepath.Join(data, "Trash", "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return err
	}
	dst := FreeName(files, filepath.Base(path))
	abs, _ := filepath.Abs(path)
	meta := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		strings.ReplaceAll(abs, " ", "%20"), time.Now().Format("2006-01-02T15:04:05"))
	if err := os.WriteFile(filepath.Join(info, filepath.Base(dst)+".trashinfo"), []byte(meta), 0o600); err != nil {
		return err
	}
	return os.Rename(path, dst)
}

// FreeName returns dir/name, or dir/"name N" when that is taken.
func FreeName(dir, name string) string {
	p := filepath.Join(dir, name)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s %d%s", base, i, ext))
	}
}
