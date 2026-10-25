// Package search finds and replaces text across a workspace, with ripgrep
// when it is installed and a Go walker that honors .gitignore otherwise.
package search

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Query is what to look for.
type Query struct {
	Pattern string
	Regex   bool // Pattern is a regular expression (RE2 syntax)
	Case    bool // match case
	Word    bool // whole words only
	// Include and Exclude are comma-separated globs such as "*.go, cmd/**".
	// An empty Include searches every file.
	Include string
	Exclude string
}

// Compile returns the query as a regular expression.
func (q Query) Compile() (*regexp.Regexp, error) {
	if q.Pattern == "" {
		return nil, errors.New("empty pattern")
	}
	p := q.Pattern
	if !q.Regex {
		p = regexp.QuoteMeta(p)
	}
	if q.Word {
		p = `\b(?:` + p + `)\b`
	}
	if !q.Case {
		p = "(?i)" + p
	}
	p = "(?m)" + p
	return regexp.Compile(p)
}

// Match is one hit.
type Match struct {
	Line     int    // 0-based
	Col      int    // byte column in the line
	Start    int    // byte offset in the file
	End      int    // byte offset in the file
	LineText string // the line, without its newline, clipped for display
	// ColInLine and EndInLine locate the match in LineText.
	ColInLine, EndInLine int
}

// FileResult holds the hits in one file.
type FileResult struct {
	Path    string
	Matches []Match
}

// Options configure a search.
type Options struct {
	Engine     string   // auto | ripgrep | builtin
	Exclude    []string // always-skipped folders and globs, from config
	MaxResults int      // stop after this many matches; 0 means 5000
	// Overlay returns the text of open buffers, which may differ from disk.
	// Files it knows are searched from it; ok=false falls back to disk.
	Overlay func(path string) (text []byte, ok bool)
	// Dirty lists buffers with unsaved changes. They are searched from
	// Overlay even when their file on disk has no match.
	Dirty []string
}

// ErrTruncated is returned with partial results when MaxResults was hit.
var ErrTruncated = errors.New("search: too many results")

// Run searches root and calls emit for each file with hits, in no
// particular order, from the calling goroutine. It stops early when ctx ends
// or emit returns false.
func Run(ctx context.Context, root string, q Query, opts Options, emit func(FileResult) bool) error {
	re, err := q.Compile()
	if err != nil {
		return err
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = 5000
	}
	f := newFilter(q, opts.Exclude)
	count := 0
	seen := map[string]bool{}
	out := func(fr FileResult) bool {
		if len(fr.Matches) == 0 {
			return true
		}
		if count+len(fr.Matches) > opts.MaxResults {
			fr.Matches = fr.Matches[:opts.MaxResults-count]
		}
		count += len(fr.Matches)
		if !emit(fr) {
			return false
		}
		return count < opts.MaxResults
	}
	// Open buffers first: their text wins over the file on disk.
	if opts.Overlay != nil {
		wrapped := out
		out = func(fr FileResult) bool {
			if seen[fr.Path] {
				return true
			}
			seen[fr.Path] = true
			if text, ok := opts.Overlay(fr.Path); ok {
				fr = FileResult{Path: fr.Path, Matches: FindAll(re, text)}
			}
			return wrapped(fr)
		}
	}
	useRG := opts.Engine == "ripgrep" || opts.Engine == "auto" || opts.Engine == ""
	if useRG {
		if _, lookErr := exec.LookPath("rg"); lookErr != nil {
			if opts.Engine == "ripgrep" {
				return errors.New("ripgrep (rg) is not installed")
			}
			useRG = false
		}
	}
	var runErr error
	if useRG {
		runErr = runRipgrep(ctx, root, q, f, out)
	} else {
		runErr = walk(ctx, root, re, f, out)
	}
	// Dirty buffers whose disk copy has no match still need searching.
	if opts.Overlay != nil && ctx.Err() == nil && count < opts.MaxResults {
		for _, p := range opts.Dirty {
			if seen[p] || !f.wants(root, p) {
				continue
			}
			if !out(FileResult{Path: p}) {
				break
			}
		}
	}
	if count >= opts.MaxResults {
		return ErrTruncated
	}
	if runErr != nil && ctx.Err() == nil {
		return runErr
	}
	return ctx.Err()
}

// maxLineDisplay clips long lines in results.
const maxLineDisplay = 400

// FindAll returns every match of re in text.
func FindAll(re *regexp.Regexp, text []byte) []Match {
	locs := re.FindAllIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}
	out := make([]Match, 0, len(locs))
	line, lineStart := 0, 0
	scanned := 0
	for _, loc := range locs {
		if loc[0] == loc[1] {
			continue // empty matches are noise in a results list
		}
		for i := scanned; i < loc[0]; i++ {
			if text[i] == '\n' {
				line++
				lineStart = i + 1
			}
		}
		scanned = loc[0]
		lineEnd := len(text)
		if i := indexByte(text[lineStart:], '\n'); i >= 0 {
			lineEnd = lineStart + i
		}
		out = append(out, makeMatch(text[lineStart:lineEnd], line, lineStart, loc[0], loc[1]))
	}
	return out
}

func makeMatch(lineText []byte, line, lineStart, start, end int) Match {
	col := start - lineStart
	endIn := min(end-lineStart, len(lineText))
	shown, from := lineText, 0
	if len(shown) > maxLineDisplay {
		// Keep the match in view: start a little before it.
		from = max(0, col-60)
		for from > 0 && !utf8.RuneStart(shown[from]) {
			from--
		}
		to := min(len(shown), from+maxLineDisplay)
		for to < len(shown) && !utf8.RuneStart(shown[to]) {
			to++
		}
		shown = shown[from:to]
	}
	return Match{
		Line: line, Col: col, Start: start, End: end,
		LineText:  strings.ToValidUTF8(string(shown), "�"),
		ColInLine: col - from, EndInLine: min(endIn-from, len(shown)),
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Sort orders results by path.
func Sort(rs []FileResult) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Path < rs[j].Path })
}
