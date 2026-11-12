// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package search

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// rgEvent is one line of `rg --json`.
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       rgText `json:"path"`
		Lines      rgText `json:"lines"`
		LineNumber int    `json:"line_number"`
		// AbsoluteOffset is the byte offset of the line in the file.
		AbsoluteOffset int `json:"absolute_offset"`
		Submatches     []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

type rgText struct {
	Text  string `json:"text"`
	Bytes string `json:"bytes"` // base64, for non-UTF-8 content
}

func (t rgText) String() string {
	if t.Bytes != "" {
		if b, err := base64.StdEncoding.DecodeString(t.Bytes); err == nil {
			return string(b)
		}
	}
	return t.Text
}

// runRipgrep is the ripgrep engine. Its regex dialect differs from Go's in
// corners; patterns Go accepts behave the same for everyday searches.
func runRipgrep(ctx context.Context, root string, q Query, f *filter, emit func(FileResult) bool) error {
	args := []string{"--json", "--hidden", "--no-require-git", "--glob", "!.git", "--max-filesize", "8M"}
	if !q.Case {
		args = append(args, "--ignore-case")
	} else {
		args = append(args, "--case-sensitive")
	}
	if !q.Regex {
		args = append(args, "--fixed-strings")
	}
	if q.Word {
		args = append(args, "--word-regexp")
	}
	if q.Regex {
		args = append(args, "--multiline")
	}
	for _, g := range f.include {
		args = append(args, "--glob", strings.TrimSpace(g.raw))
	}
	for _, g := range f.exclude {
		args = append(args, "--glob", "!"+strings.TrimSpace(g.raw))
	}
	args = append(args, "--regexp", q.Pattern, "--", ".")
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var cur *FileResult
	stopped := false
	flush := func() {
		if cur != nil && len(cur.Matches) > 0 && !stopped {
			if !emit(*cur) {
				stopped = true
			}
		}
		cur = nil
	}
	for sc.Scan() && !stopped {
		var ev rgEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "begin":
			flush()
			cur = &FileResult{Path: filepath.Join(root, ev.Data.Path.String())}
		case "match":
			if cur == nil {
				continue
			}
			text := strings.TrimRight(ev.Data.Lines.String(), "\n")
			// A multiline match spans lines; show its first.
			first := text
			if i := strings.IndexByte(first, '\n'); i >= 0 {
				first = first[:i]
			}
			for _, sm := range ev.Data.Submatches {
				m := makeMatch([]byte(first), ev.Data.LineNumber-1, ev.Data.AbsoluteOffset,
					ev.Data.AbsoluteOffset+sm.Start, ev.Data.AbsoluteOffset+sm.End)
				if sm.Start > len(first) {
					m.Line += strings.Count(text[:sm.Start], "\n")
				}
				cur.Matches = append(cur.Matches, m)
			}
		case "end":
			flush()
		}
	}
	flush()
	if stopped {
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	var xe *exec.ExitError
	if errors.As(err, &xe) && xe.ExitCode() == 1 {
		return nil // no matches
	}
	if err != nil && !stopped && ctx.Err() == nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New("rg: " + msg)
		}
		return err
	}
	return nil
}
