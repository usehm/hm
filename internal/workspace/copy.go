package workspace

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mirzakhany/hm/internal/trash"
)

// CopyInto copies the file or folder src into dir and returns the new path.
// When the name is taken there, the copy is named "name copy.ext", then
// "name copy 2.ext" and so on, so pasting next to the original works.
func CopyInto(src, dir string) (string, error) {
	if err := checkInto(src, dir); err != nil {
		return "", err
	}
	dst := copyName(dir, filepath.Base(src))
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	return dst, nil
}

// MoveInto moves the file or folder src into dir and returns the new path,
// src itself when it is already there. A taken name gets a number, as
// Trash does.
func MoveInto(src, dir string) (string, error) {
	if filepath.Dir(src) == filepath.Clean(dir) {
		return src, nil
	}
	if err := checkInto(src, dir); err != nil {
		return "", err
	}
	dst := trash.FreeName(dir, filepath.Base(src))
	return dst, os.Rename(src, dst)
}

// checkInto refuses to put a folder inside itself.
func checkInto(src, dir string) error {
	src, dir = filepath.Clean(src), filepath.Clean(dir)
	if _, ok := under(dir, src); ok || dir == src {
		return fmt.Errorf("cannot put %s inside itself", filepath.Base(src))
	}
	return nil
}

// copyName returns dir/name, or the first free "name copy N" beside it.
func copyName(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Lstat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if base == "" { // a dotfile such as .env
		base, ext = name, ""
	}
	for i := 1; ; i++ {
		suffix := " copy"
		if i > 1 {
			suffix = fmt.Sprintf(" copy %d", i)
		}
		p = filepath.Join(dir, base+suffix+ext)
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p
		}
	}
}

// copyTree copies src to dst, keeping file modes and symlinks.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		default:
			return copyFile(p, target, info.Mode().Perm())
		}
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
