// Adapted from chapar ui/pages/gititems.go (github.com/chapar-rest/chapar@2342e52a).

package gitui

import (
	"bytes"
	"path"

	"github.com/mirzakhany/yoga/highlight"
	"github.com/mirzakhany/yoga/icons"
)

// gitItem names a changed file for the list.
type gitItem struct {
	Name  string // file name
	Where string // its folder, relative to the workspace, or ""
	Type  string // always "File"; kept so the layout code reads like chapar's
}

// itemFor names the file at the workspace-relative path p.
func itemFor(p string) gitItem {
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	}
	return gitItem{Name: path.Base(p), Where: dir, Type: "File"}
}

func (it gitItem) icon() icons.Icon { return icons.File }

// label is the file name with its folder.
func (it gitItem) label() string {
	if it.Where != "" {
		return it.Where + "/" + it.Name
	}
	return it.Name
}

// diffTokens colors a unified diff by line: added and removed lines, hunk
// headers, and the file header lines git writes before the first hunk.
func diffTokens(data []byte) []highlight.Token {
	var toks []highlight.Token
	inHeader := false
	for start := 0; start < len(data); {
		end := bytes.IndexByte(data[start:], '\n')
		if end < 0 {
			end = len(data)
		} else {
			end += start
		}
		line := data[start:end]
		class, ok := highlight.ClassDefault, false
		switch {
		case bytes.HasPrefix(line, []byte("diff ")):
			inHeader = true
			class, ok = highlight.ClassMuted, true
		case bytes.HasPrefix(line, []byte("@@")):
			inHeader = false
			class, ok = highlight.ClassType, true
		case inHeader:
			class, ok = highlight.ClassMuted, true
		case len(line) > 0 && line[0] == '+':
			class, ok = highlight.ClassSuccess, true
		case len(line) > 0 && line[0] == '-':
			class, ok = highlight.ClassError, true
		case len(line) > 0 && line[0] == '\\':
			class, ok = highlight.ClassMuted, true
		}
		if ok && end > start {
			toks = append(toks, highlight.Token{Start: start, End: end, Class: class})
		}
		start = end + 1
	}
	return toks
}

// staticHighlighter hands the editor tokens worked out up front. The diff
// editors are read-only and rebuilt for new content, so the tokens never
// go stale.
type staticHighlighter struct {
	tokens []highlight.Token
	sent   bool
}

func (h *staticHighlighter) Update([]byte)                     {}
func (h *staticHighlighter) UpdateEdit([]byte, highlight.Edit) {}
func (h *staticHighlighter) Close()                            {}
func (h *staticHighlighter) Poll() ([]highlight.Token, bool) {
	if h.sent {
		return nil, false
	}
	h.sent = true
	return h.tokens, true
}
