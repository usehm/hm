// Package complete asks a language model for an inline completion at the
// caret: it builds the prompt from the text around the caret and cleans the
// reply into the text to insert.
package complete

import (
	"context"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

// How much of the file goes into the prompt, in bytes on each side.
const (
	maxBefore = 6000
	maxAfter  = 2000
)

// Cursor marks the caret in the prompt.
const Cursor = "<CURSOR>"

const system = `You are the inline code completion engine of a code editor.
The user message holds the file being edited, with ` + Cursor + ` where the caret is.
Reply with only the text to insert at the caret: no explanation, no markdown
fences, and nothing that is already before or after the caret. Finish the
current line, or add the next few lines when the code clearly calls for them,
and match the file's indentation and style. If nothing useful fits, reply with
an empty message.`

// Request is one completion to ask for.
type Request struct {
	Path      string // shown to the model; relative to the workspace
	Text      []byte // the whole document
	Caret     int    // byte offset in Text
	Model     string
	MaxTokens int
}

// Eligible reports whether the caret is a place to offer a completion
// unasked: nothing but whitespace and closing punctuation follows it on its
// line, and the document is not empty.
func Eligible(text []byte, caret int) bool {
	if caret < 0 || caret > len(text) || len(strings.TrimSpace(string(text))) == 0 {
		return false
	}
	rest := text[caret:]
	if i := strings.IndexByte(string(rest), '\n'); i >= 0 {
		rest = rest[:i]
	}
	for _, r := range string(rest) {
		if !unicode.IsSpace(r) && !strings.ContainsRune(")]}>\"'`;,", r) {
			return false
		}
	}
	return true
}

// Prompt builds the user message for req.
func Prompt(req Request) string {
	before, after := around(req.Text, req.Caret)
	var b strings.Builder
	b.WriteString("File: ")
	b.WriteString(filepath.ToSlash(req.Path))
	b.WriteString("\n\n")
	b.WriteString(before)
	b.WriteString(Cursor)
	b.WriteString(after)
	return b.String()
}

// around returns the text before and after the caret that fits the prompt,
// cut at line boundaries.
func around(text []byte, caret int) (before, after string) {
	lo := max(caret-maxBefore, 0)
	if lo > 0 {
		if i := strings.IndexByte(string(text[lo:caret]), '\n'); i >= 0 {
			lo += i + 1
		}
	}
	hi := min(caret+maxAfter, len(text))
	if hi < len(text) {
		if i := strings.LastIndexByte(string(text[caret:hi]), '\n'); i >= 0 {
			hi = caret + i
		}
	}
	return string(text[lo:caret]), string(text[caret:hi])
}

// Ask runs req against p and returns the text to insert, or "".
func Ask(ctx context.Context, p provider.Provider, req Request) (string, error) {
	resp, err := p.Stream(ctx, provider.Request{
		Model:     req.Model,
		System:    system,
		Messages:  []provider.Message{{Role: provider.User, Blocks: []provider.Block{{Type: provider.Text, Text: Prompt(req)}}}},
		MaxTokens: req.MaxTokens,
	}, nil)
	if err != nil {
		return "", err
	}
	before, after := around(req.Text, req.Caret)
	return Clean(resp.Message.TextOf(), before, after), nil
}

// Clean turns a model reply into the text to insert between before and
// after: it drops markdown fences and the cursor marker, the part of the
// current line the model repeated, and the closing text that already
// follows the caret.
func Clean(out, before, after string) string {
	out = strings.ReplaceAll(out, Cursor, "")
	out = unfence(out)
	out = strings.TrimRight(out, " \t\r\n")

	// The model may restate the line it was asked to finish.
	line := before[strings.LastIndexByte(before, '\n')+1:]
	if typed := strings.TrimLeft(line, " \t"); typed != "" && strings.HasPrefix(out, typed) {
		out = out[len(typed):]
	} else if line != "" && strings.TrimSpace(line) == "" {
		// On an indented empty line, keep the reply's first line from
		// indenting twice.
		out = strings.TrimPrefix(out, line)
	}

	// Or end with what already closes the line, such as ")".
	rest := after
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	if rest = strings.TrimSpace(rest); rest != "" {
		out = strings.TrimSuffix(out, rest)
	}
	if strings.TrimSpace(out) == "" {
		return ""
	}
	return out
}

// unfence strips a markdown code fence around the whole reply.
func unfence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	nl := strings.IndexByte(t, '\n')
	if nl < 0 {
		return ""
	}
	t = t[nl+1:]
	if i := strings.LastIndex(t, "```"); i >= 0 {
		t = t[:i]
	}
	return strings.TrimRight(t, "\n")
}
