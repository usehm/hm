// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Package prompts loads prompt templates: Markdown files that become slash
// commands in the agent's message box. The file name is the command, the
// body is the message sent, with $ARGUMENTS, $1, $2 ... filled from what is
// typed after the command and @path lines replaced by the file's content.
// Claude Code's .claude/commands files use the same format.
package prompts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/usehm/hm/internal/config"
)

// Template is one loaded prompt template.
type Template struct {
	Name         string
	Description  string
	ArgumentHint string
	// Mode is the agent mode to switch to before sending; Model picks the
	// provider to switch to, by its name or the model it runs.
	Mode  string
	Model string
	Path  string // file on disk
	Group string // subfolder the file is in, if any, such as "frontend"
	Body  string // the file after the front matter
}

// Load reads the templates in dirs and their subfolders; a later folder's
// template replaces an earlier one with the same name.
func Load(dirs []string) ([]Template, []error) {
	byName := map[string]Template{}
	var errs []error
	for _, dir := range dirs {
		var found []Template
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			t, err := Read(p)
			if err != nil {
				errs = append(errs, err)
				return nil
			}
			if rel, err := filepath.Rel(dir, filepath.Dir(p)); err == nil && rel != "." {
				t.Group = filepath.ToSlash(rel)
			}
			found = append(found, t)
			return nil
		})
		// Within one folder a file at the top beats one in a subfolder.
		sort.SliceStable(found, func(i, j int) bool { return found[i].Group > found[j].Group })
		for _, t := range found {
			byName[t.Name] = t
		}
	}
	out := make([]Template, 0, len(byName))
	for _, t := range byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs
}

// Read reads one template file.
func Read(path string) (Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Template{}, err
	}
	t, err := parse(string(data))
	if err != nil {
		return Template{}, fmt.Errorf("%s: %w", path, err)
	}
	t.Name = strings.TrimSuffix(filepath.Base(path), ".md")
	t.Path = path
	return t, nil
}

// parse splits a template into front matter and body. Keys hm does not
// use, such as Claude Code's allowed-tools, are ignored.
func parse(text string) (Template, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Template{Body: strings.TrimSpace(text)}, nil
	}
	rest := text[len("---\n"):]
	var front, body string
	switch {
	case strings.HasPrefix(rest, "---\n") || rest == "---":
		body = strings.TrimPrefix(rest, "---")
	default:
		i := strings.Index(rest, "\n---\n")
		if i < 0 {
			if !strings.HasSuffix(rest, "\n---") {
				return Template{}, errors.New("front matter is not closed with ---")
			}
			i = len(rest) - len("\n---")
		}
		front, body = rest[:i], rest[min(i+len("\n---\n"), len(rest)):]
	}
	var meta struct {
		Description  string `yaml:"description"`
		ArgumentHint string `yaml:"argument-hint"`
		Mode         string `yaml:"mode"`
		Model        string `yaml:"model"`
	}
	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		return Template{}, err
	}
	return Template{
		Description:  strings.TrimSpace(meta.Description),
		ArgumentHint: strings.TrimSpace(meta.ArgumentHint),
		Mode:         strings.TrimSpace(meta.Mode),
		Model:        strings.TrimSpace(meta.Model),
		Body:         strings.TrimSpace(body),
	}, nil
}

// Dirs resolves the configured template paths against the workspace root.
func Dirs(paths []string, root string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = config.ExpandHome(p)
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		out = append(out, p)
	}
	return out
}

// Expand fills in the template's body. args is everything typed after the
// command. include reads the file an @path line names; when it fails the
// line stays as written and its error is returned as a warning.
func (t Template) Expand(args string, include func(path string) (string, error)) (string, []string) {
	text := fill(t.Body, strings.TrimSpace(args), Split(args))
	var warnings []string
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if len(s) < 2 || s[0] != '@' || strings.ContainsAny(s, " \t") {
			continue
		}
		data, err := include(s[1:])
		if err != nil {
			warnings = append(warnings, s+": "+err.Error())
			continue
		}
		lines[i] = strings.TrimRight(data, "\n")
	}
	return strings.Join(lines, "\n"), warnings
}

// fill replaces $ARGUMENTS with all and $1, $2 ... with the positional
// arguments, a missing one with nothing. Any other $ stays as it is.
func fill(body, all string, pos []string) string {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '$' {
			b.WriteByte(body[i])
			continue
		}
		rest := body[i+1:]
		if strings.HasPrefix(rest, "ARGUMENTS") {
			b.WriteString(all)
			i += len("ARGUMENTS")
			continue
		}
		n := 0
		for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
			n++
		}
		k, _ := strconv.Atoi(rest[:n])
		if n == 0 || k == 0 {
			b.WriteByte('$')
			continue
		}
		if k <= len(pos) {
			b.WriteString(pos[k-1])
		}
		i += n
	}
	return b.String()
}

// Split cuts arguments into words the way a shell does: at spaces outside
// quotes, with single quotes taken literally and a backslash escaping the
// next character outside them.
func Split(s string) []string {
	var out []string
	var cur strings.Builder
	word := false
	var quote rune
	esc := false
	for _, c := range s {
		switch {
		case esc:
			cur.WriteRune(c)
			esc = false
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\\':
			esc, word = true, true
		case quote == '"':
			if c == '"' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, word = c, true
		case c == ' ' || c == '\t' || c == '\n':
			if word {
				out = append(out, cur.String())
				cur.Reset()
				word = false
			}
		default:
			cur.WriteRune(c)
			word = true
		}
	}
	if word {
		out = append(out, cur.String())
	}
	return out
}
