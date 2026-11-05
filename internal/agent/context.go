package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContextFile is an instruction file given to the agent, such as a
// project's AGENTS.md.
type ContextFile struct {
	Path string
	// Rel is how the file is named to the agent and the user: relative to
	// the workspace, ~/ for the home folder, else absolute.
	Rel     string
	Content string
}

// Context is the instruction files found for a workspace, read once when a
// session starts so its system prompt stays the same and caches.
type Context struct {
	Files []ContextFile
	// System is the SYSTEM.md in use, nil when there is none. It replaces
	// the base prompt, or adds to it when Append is set.
	System *ContextFile
	Append bool
	// Warnings are files that were skipped, said in plain words.
	Warnings []string
}

// ContextOptions are the agent.context settings.
type ContextOptions struct {
	// Names are the file names looked for in each folder; the first one
	// found wins, so later names are fallbacks.
	Names    []string
	MaxBytes int
}

// appendMarker, as SYSTEM.md's first line, adds the file to the base
// prompt instead of replacing it.
const appendMarker = "<!-- append -->"

// LoadContext finds the instruction files for root: the user's file in
// userDir, then one per folder from the home folder (or the filesystem
// root, when root is outside home) down to root, outer folders first. It
// also picks SYSTEM.md: root/.hm/SYSTEM.md, else the one in userDir.
func LoadContext(root, home, userDir string, o ContextOptions) Context {
	var c Context
	seen := map[string]bool{}
	name := func(p string) string { return displayPath(p, root, home) }
	read := func(p string) (string, bool) {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			return "", false
		}
		if o.MaxBytes > 0 && info.Size() > int64(o.MaxBytes) {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s is %d KiB, over the %d KiB limit (agent.context.max_bytes); skipped",
				name(p), info.Size()/1024, o.MaxBytes/1024))
			return "", false
		}
		data, err := os.ReadFile(p)
		if err != nil {
			c.Warnings = append(c.Warnings, name(p)+": "+err.Error())
			return "", false
		}
		return string(data), true
	}
	for _, dir := range append([]string{userDir}, contextDirs(root, home)...) {
		for _, n := range o.Names {
			p := filepath.Join(dir, n)
			if seen[p] {
				break
			}
			text, ok := read(p)
			if !ok {
				continue
			}
			seen[p] = true
			if strings.TrimSpace(text) != "" {
				c.Files = append(c.Files, ContextFile{Path: p, Rel: name(p), Content: text})
			}
			break
		}
	}
	for _, p := range []string{filepath.Join(root, ".hm", "SYSTEM.md"), filepath.Join(userDir, "SYSTEM.md")} {
		text, ok := read(p)
		if !ok {
			continue
		}
		first, rest, _ := strings.Cut(text, "\n")
		if strings.TrimSpace(first) == appendMarker {
			c.Append, text = true, rest
		}
		c.System = &ContextFile{Path: p, Rel: name(p), Content: text}
		break
	}
	return c
}

// contextDirs lists the folders from home, or the filesystem root when
// root is not under home, down to root.
func contextDirs(root, home string) []string {
	root = filepath.Clean(root)
	stop := ""
	if home != "" && within(root, filepath.Clean(home)) {
		stop = filepath.Clean(home)
	}
	var dirs []string
	for d := root; ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if d == stop || filepath.Dir(d) == d {
			break
		}
	}
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func displayPath(p, root, home string) string {
	if within(p, root) {
		if rel, err := filepath.Rel(root, p); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	if home != "" && within(p, home) {
		if rel, err := filepath.Rel(home, p); err == nil {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

// Base is the start of the system prompt: hm's own, or SYSTEM.md's
// replacement or addition.
func (c *Context) Base() string {
	if c == nil || c.System == nil {
		return basePrompt
	}
	text := strings.TrimSpace(c.System.Content)
	if c.Append {
		return basePrompt + "\n\n" + text
	}
	return text
}

// Prompt is the instruction files' part of the system prompt; empty when
// there are none.
func (c *Context) Prompt() string {
	if c == nil || len(c.Files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Project instructions\nThe user keeps these files of instructions for working here, outer folders first; when they disagree, the later, more specific file wins.\n")
	for _, f := range c.Files {
		fmt.Fprintf(&b, "\n## %s\n%s\n", f.Rel, strings.TrimSpace(f.Content))
	}
	return b.String()
}

// Loaded lists every file in use, SYSTEM.md first.
func (c *Context) Loaded() []ContextFile {
	if c == nil {
		return nil
	}
	var out []ContextFile
	if c.System != nil {
		out = append(out, *c.System)
	}
	return append(out, c.Files...)
}
