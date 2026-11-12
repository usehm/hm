// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/usehm/hm/internal/agent/tools"
	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/search"
	"github.com/usehm/hm/internal/trash"
)

// diskEnv is the agent's workspace without an editor: files are read from
// disk and there are no language servers.
type diskEnv struct {
	root   string
	search config.Search
}

func (e diskEnv) Root() string { return e.root }

func (e diskEnv) ReadFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a folder; use ls", path)
	}
	return os.ReadFile(path)
}

func (e diskEnv) Search(ctx context.Context, q search.Query, max int) ([]search.FileResult, error) {
	var out []search.FileResult
	err := search.Run(ctx, e.root, q, search.Options{Engine: e.search.Engine, Exclude: e.search.Exclude, MaxResults: max},
		func(fr search.FileResult) bool { out = append(out, fr); return true })
	search.Sort(out)
	return out, err
}

func (diskEnv) Diagnostics(string) []string { return nil }

// applyEdits writes the agent's edits to disk; a deleted file goes to the
// trash, so it can be got back.
func applyEdits(edits []tools.FileEdit) error {
	for _, e := range edits {
		if e.New == nil {
			if _, err := os.Lstat(e.Path); errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err := trash.Move(e.Path); err != nil {
				return err
			}
			continue
		}
		mode := fs.FileMode(0o644)
		if st, err := os.Stat(e.Path); err == nil {
			mode = st.Mode().Perm()
		}
		if err := os.MkdirAll(filepath.Dir(e.Path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(e.Path, e.New, mode); err != nil {
			return err
		}
	}
	return nil
}
