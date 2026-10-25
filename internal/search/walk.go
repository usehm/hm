package search

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
)

// maxFileSize skips files too big to be source.
const maxFileSize = 8 << 20

// walk is the built-in engine: a recursive walk that honors .gitignore.
func walk(ctx context.Context, root string, re *regexp.Regexp, f *filter, emit func(FileResult) bool) error {
	var visit func(dir string, ig ignores) bool
	visit = func(dir string, ig ignores) bool {
		if ctx.Err() != nil {
			return false
		}
		ig = ig.load(root, dir)
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			return true
		}
		for _, e := range entries {
			name := e.Name()
			rel := filepath.Join(dir, name)
			if dir == "." {
				rel = name
			}
			if name == ".git" {
				continue
			}
			if e.IsDir() {
				if f.skipDir(rel) || ig.ignored(rel, true) {
					continue
				}
				if !visit(rel, ig) {
					return false
				}
				continue
			}
			if !e.Type().IsRegular() || ig.ignored(rel, false) {
				continue
			}
			path := filepath.Join(root, rel)
			if !f.wants(root, path) {
				continue
			}
			matches := searchFile(path, re)
			if len(matches) > 0 && !emit(FileResult{Path: path, Matches: matches}) {
				return false
			}
		}
		return true
	}
	visit(".", nil)
	return nil
}

func searchFile(path string, re *regexp.Regexp) []Match {
	st, err := os.Stat(path)
	if err != nil || st.Size() > maxFileSize {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || isBinary(data) {
		return nil
	}
	return FindAll(re, data)
}

func isBinary(data []byte) bool {
	head := data[:min(len(data), 8000)]
	return bytes.IndexByte(head, 0) >= 0
}
