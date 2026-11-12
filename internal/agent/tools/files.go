package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/usehm/hm/internal/agent/images"
	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/search"
)

// --- read -------------------------------------------------------------------

type readTool struct{}

const readMaxLines = 2000

func (readTool) Spec() provider.Tool {
	return provider.Tool{Name: "read", Description: "Read a text file. Lines come numbered (1-based). " +
		"Open buffers are read with their unsaved changes. Use offset and limit for large files. " +
		"PNG, JPEG, GIF and WebP files come back as images you can see.",
		Schema: schema(map[string]any{
			"path":   str("File path, relative to the workspace root or absolute"),
			"offset": integer("First line to read, 1-based (default 1)"),
			"limit":  integer(fmt.Sprintf("Number of lines (default %d)", readMaxLines)),
		}, "path")}
}

func (readTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Path          string `json:"path"`
		Offset, Limit int
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	path := resolve(env, in.Path)
	return Plan{Category: CatRead, Summary: "Read " + rel(env, path), Paths: []string{path}, Run: func(context.Context) Result {
		data, err := env.ReadFile(path)
		if err != nil {
			return fail("%v", err)
		}
		if images.IsImagePath(path) || images.Sniff(data) != "" {
			if r, ok := readImage(rel(env, path), data); ok {
				return r
			}
		}
		if isBinary(data) {
			return ok("%s is a binary file (%s)", rel(env, path), size(len(data)))
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		start := max(in.Offset, 1) - 1
		limit := in.Limit
		if limit <= 0 {
			limit = readMaxLines
		}
		if start >= len(lines) {
			return ok("%s has %d lines", rel(env, path), len(lines))
		}
		end := min(start+limit, len(lines))
		var b strings.Builder
		for i := start; i < end; i++ {
			line := lines[i]
			if len(line) > 2000 {
				line = line[:2000] + "…"
			}
			fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
		}
		if end < len(lines) {
			fmt.Fprintf(&b, "… %d more lines; read again with offset %d\n", len(lines)-end, end+1)
		}
		return ok("%s", b.String())
	}}, nil
}

// readImage gives the model a picture, scaled down when it is large.
func readImage(name string, data []byte) (Result, bool) {
	im, err := images.Load(data)
	if err != nil {
		return Result{}, false
	}
	text := fmt.Sprintf("Image %s, %d×%d", name, im.OrigWidth, im.OrigHeight)
	if im.Resized() {
		text += fmt.Sprintf(" (sent scaled to %d×%d)", im.Width, im.Height)
	}
	return Result{Text: text, Images: []provider.ImageData{im.Message()}}, true
}

// size is a byte count for people.
func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

func isBinary(data []byte) bool {
	head := data[:min(len(data), 8000)]
	for _, c := range head {
		if c == 0 {
			return true
		}
	}
	return false
}

// --- write ------------------------------------------------------------------

type writeTool struct{}

func (writeTool) Spec() provider.Tool {
	return provider.Tool{Name: "write", Description: "Create a file or replace all of its content. " +
		"Prefer edit for changes to existing files.",
		Schema: schema(map[string]any{
			"path":    str("File path"),
			"content": str("The whole new content"),
		}, "path", "content")}
}

func (writeTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct{ Path, Content string }
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	path := resolve(env, in.Path)
	old, err := env.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		old = nil
	} else if err != nil {
		return Plan{}, err
	}
	verb := "Write"
	if old == nil {
		verb = "Create"
	}
	return Plan{Category: CatEdit, Summary: verb + " " + rel(env, path), Paths: []string{path},
		Edits: []FileEdit{{Path: path, Old: old, New: []byte(in.Content)}}}, nil
}

// --- edit / multi_edit ------------------------------------------------------

type editTool struct{}

type editSpec struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

var editProps = map[string]any{
	"old_string":  str("Exact text to replace, including indentation; it must appear once unless replace_all is set"),
	"new_string":  str("The replacement text"),
	"replace_all": boolean("Replace every occurrence (default false)"),
}

func (editTool) Spec() provider.Tool {
	props := map[string]any{"path": str("File path")}
	for k, v := range editProps {
		props[k] = v
	}
	return provider.Tool{Name: "edit", Description: "Replace an exact string in a file. Read the file first; " +
		"old_string must match byte for byte and be unique unless replace_all is set.",
		Schema: schema(props, "path", "old_string", "new_string")}
}

func (editTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Path string `json:"path"`
		editSpec
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	return planEdits(env, in.Path, []editSpec{in.editSpec})
}

type multiEditTool struct{}

func (multiEditTool) Spec() provider.Tool {
	return provider.Tool{Name: "multi_edit", Description: "Make several exact-string replacements in one file, " +
		"in order, as one change. Each edit sees the result of the ones before it.",
		Schema: schema(map[string]any{
			"path": str("File path"),
			"edits": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{
				"type": "object", "properties": editProps, "required": []string{"old_string", "new_string"},
				"additionalProperties": false,
			}},
		}, "path", "edits")}
}

func (multiEditTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Path  string     `json:"path"`
		Edits []editSpec `json:"edits"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	return planEdits(env, in.Path, in.Edits)
}

// planEdits applies string replacements in memory and plans the result.
func planEdits(env Env, p string, edits []editSpec) (Plan, error) {
	path := resolve(env, p)
	data, err := env.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	text := string(data)
	for i, e := range edits {
		if e.OldString == "" {
			return Plan{}, fmt.Errorf("edit %d: old_string is empty; use write to create a file", i+1)
		}
		if e.OldString == e.NewString {
			return Plan{}, fmt.Errorf("edit %d: old_string and new_string are the same", i+1)
		}
		n := strings.Count(text, e.OldString)
		switch {
		case n == 0:
			return Plan{}, fmt.Errorf("edit %d: old_string was not found in %s; read the file and copy the text exactly", i+1, rel(env, path))
		case n > 1 && !e.ReplaceAll:
			return Plan{}, fmt.Errorf("edit %d: old_string appears %d times in %s; add surrounding lines to make it unique, or set replace_all", i+1, n, rel(env, path))
		}
		if e.ReplaceAll {
			text = strings.ReplaceAll(text, e.OldString, e.NewString)
		} else {
			text = strings.Replace(text, e.OldString, e.NewString, 1)
		}
	}
	return Plan{Category: CatEdit, Summary: "Edit " + rel(env, path), Paths: []string{path},
		Edits: []FileEdit{{Path: path, Old: data, New: []byte(text)}}}, nil
}

// --- delete -----------------------------------------------------------------

type deleteTool struct{}

func (deleteTool) Spec() provider.Tool {
	return provider.Tool{Name: "delete", Description: "Delete a file (moved to the trash).",
		Schema: schema(map[string]any{"path": str("File path")}, "path")}
}

func (deleteTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct{ Path string }
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	path := resolve(env, in.Path)
	old, err := env.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Category: CatDelete, Summary: "Delete " + rel(env, path), Paths: []string{path},
		Edits: []FileEdit{{Path: path, Old: old, New: nil}}}, nil
}

// --- ls ---------------------------------------------------------------------

type lsTool struct{}

func (lsTool) Spec() provider.Tool {
	return provider.Tool{Name: "ls", Description: "List a folder's entries; folders end with /.",
		Schema: schema(map[string]any{"path": str("Folder, relative to the workspace root (default the root)")})}
}

func (lsTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct{ Path string }
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	dir := resolve(env, in.Path)
	return Plan{Category: CatRead, Summary: "List " + rel(env, dir), Paths: []string{dir}, Run: func(context.Context) Result {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fail("%v", err)
		}
		var b strings.Builder
		for _, e := range entries {
			if e.Name() == ".git" {
				continue
			}
			b.WriteString(e.Name())
			if e.IsDir() {
				b.WriteString("/")
			}
			b.WriteString("\n")
		}
		if b.Len() == 0 {
			return ok("(empty)")
		}
		return ok("%s", clip(b.String(), 30000))
	}}, nil
}

// --- glob -------------------------------------------------------------------

type globTool struct{}

var skipNames = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true}

func (globTool) Spec() provider.Tool {
	return provider.Tool{Name: "glob", Description: "Find files by name pattern, such as **/*_test.go or cmd/**.",
		Schema: schema(map[string]any{
			"pattern": str("Glob pattern; a pattern without / matches names at any depth"),
			"path":    str("Folder to search (default the workspace root)"),
		}, "pattern")}
}

func (globTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct{ Pattern, Path string }
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	dir := resolve(env, in.Path)
	return Plan{Category: CatRead, Summary: "Find " + in.Pattern, Paths: []string{dir}, Run: func(ctx context.Context) Result {
		var out []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil {
				return filepath.SkipAll
			}
			if d.IsDir() && p != dir && skipNames[d.Name()] {
				return filepath.SkipDir
			}
			if d.IsDir() {
				return nil
			}
			r, _ := filepath.Rel(dir, p)
			if search.MatchGlob(in.Pattern, filepath.ToSlash(r)) {
				out = append(out, rel(env, p))
			}
			if len(out) >= 1000 {
				return filepath.SkipAll
			}
			return nil
		})
		sort.Strings(out)
		if len(out) == 0 {
			return ok("No files match %s", in.Pattern)
		}
		return ok("%s", strings.Join(out, "\n"))
	}}, nil
}

// --- grep / search_project --------------------------------------------------

type grepTool struct{}

func (grepTool) Spec() provider.Tool {
	return provider.Tool{Name: "grep", Description: "Search file contents with a regular expression (RE2). " +
		"Results are path:line: text. Honors .gitignore.",
		Schema: schema(map[string]any{
			"pattern":          str("Regular expression"),
			"include":          str("Comma-separated globs of files to search, such as *.go"),
			"case_insensitive": boolean("Ignore case"),
		}, "pattern")}
}

func (grepTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Pattern         string `json:"pattern"`
		Include         string `json:"include"`
		CaseInsensitive bool   `json:"case_insensitive"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	q := search.Query{Pattern: in.Pattern, Regex: true, Case: !in.CaseInsensitive, Include: in.Include}
	return searchPlan(env, "Search for /"+in.Pattern+"/", q), nil
}

type searchTool struct{}

func (searchTool) Spec() provider.Tool {
	return provider.Tool{Name: "search_project", Description: "Search the workspace for literal text, " +
		"the way the editor's search does, including unsaved buffers.",
		Schema: schema(map[string]any{
			"query":      str("Text to find"),
			"whole_word": boolean("Match whole words only"),
			"include":    str("Comma-separated globs of files to search"),
		}, "query")}
}

func (searchTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Query     string `json:"query"`
		WholeWord bool   `json:"whole_word"`
		Include   string `json:"include"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	q := search.Query{Pattern: in.Query, Word: in.WholeWord, Include: in.Include}
	return searchPlan(env, "Search for "+in.Query, q), nil
}

const searchMaxResults = 300

func searchPlan(env Env, summary string, q search.Query) Plan {
	return Plan{Category: CatRead, Summary: summary, Run: func(ctx context.Context) Result {
		if _, err := q.Compile(); err != nil {
			return fail("%v", err)
		}
		results, err := env.Search(ctx, q, searchMaxResults)
		if err != nil && !errors.Is(err, search.ErrTruncated) {
			return fail("%v", err)
		}
		var b strings.Builder
		n := 0
		for _, fr := range results {
			for _, m := range fr.Matches {
				fmt.Fprintf(&b, "%s:%d: %s\n", rel(env, fr.Path), m.Line+1, strings.TrimSpace(m.LineText))
				n++
			}
		}
		if n == 0 {
			return ok("No matches")
		}
		if errors.Is(err, search.ErrTruncated) {
			fmt.Fprintf(&b, "… stopped at %d matches; narrow the search\n", searchMaxResults)
		}
		return ok("%s", clip(b.String(), 40000))
	}}
}
