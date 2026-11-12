// Package skills loads agent skills: folders holding a SKILL.md with name
// and description front matter, plus any files the skill refers to. Every
// skill's description goes into the system prompt; the body loads when the
// model asks for it with the skill tool, or up front when the mode lists it.
package skills

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/agent/tools"
	"github.com/usehm/hm/internal/config"
)

//go:embed all:builtin
var builtinFS embed.FS

// Skill is one loaded skill.
type Skill struct {
	Name        string
	Description string
	Dir         string // folder on disk
	Body        string // SKILL.md after the front matter
}

// Files lists the skill's other files, relative to its folder.
func (s Skill) Files() []string {
	var out []string
	_ = filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if rel, err := filepath.Rel(s.Dir, p); err == nil && rel != "SKILL.md" {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Materialize writes the built-in skills under dir, replacing older copies,
// so their files are on disk for the agent to read and run.
func Materialize(dir string) error {
	return fs.WalkDir(builtinFS, "builtin", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "builtin"), "/")
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := builtinFS.ReadFile(p)
		if err != nil {
			return err
		}
		if old, err := os.ReadFile(target); err == nil && bytes.Equal(old, data) {
			return nil
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}

// Load reads the skills found in dirs (each holding skill folders); a
// later folder's skill replaces an earlier one with the same name.
func Load(dirs []string) ([]Skill, []error) {
	byName := map[string]Skill{}
	var errs []error
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sdir := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(filepath.Join(sdir, "SKILL.md"))
			if err != nil {
				continue
			}
			s, err := parse(data)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", filepath.Join(sdir, "SKILL.md"), err))
				continue
			}
			if s.Name == "" {
				s.Name = e.Name()
			}
			s.Dir = sdir
			byName[s.Name] = s
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs
}

// parse splits SKILL.md into front matter and body.
func parse(data []byte) (Skill, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return Skill{Body: text}, nil
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	sc.Scan() // opening ---
	var front strings.Builder
	closed := false
	n := len(strings.SplitN(text, "\n", 2)[0]) + 1
	for sc.Scan() {
		line := sc.Text()
		n += len(line) + 1
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		front.WriteString(line + "\n")
	}
	if !closed {
		return Skill{}, errors.New("front matter is not closed with ---")
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(front.String()), &meta); err != nil {
		return Skill{}, err
	}
	body := ""
	if n < len(text) {
		body = text[n:]
	}
	return Skill{Name: meta.Name, Description: strings.TrimSpace(meta.Description), Body: strings.TrimSpace(body)}, nil
}

// Dirs resolves the configured skill paths against the workspace root, with
// the built-in skills first so the user's win.
func Dirs(builtin string, paths []string, root string) []string {
	out := []string{builtin}
	for _, p := range paths {
		p = config.ExpandHome(p)
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		out = append(out, p)
	}
	return out
}

// Prompt lists the skills for the system prompt and inlines the bodies of
// the ones the mode preloads.
func Prompt(all []Skill, preload []string) string {
	if len(all) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Skills\nSkills hold know-how and files for particular jobs. Load one with the skill tool when the task matches its description.\n")
	for _, s := range all {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
	}
	for _, name := range preload {
		for _, s := range all {
			if s.Name == name {
				fmt.Fprintf(&b, "\n## Skill %s (loaded)\nFolder: %s\n\n%s\n", s.Name, s.Dir, s.Body)
			}
		}
	}
	return b.String()
}

// Tool returns the skill tool, which loads a skill's instructions or one of
// its files.
func Tool(all []Skill) tools.Tool { return skillTool{all: all} }

type skillTool struct{ all []Skill }

func (t skillTool) Spec() provider.Tool {
	names := make([]string, len(t.all))
	for i, s := range t.all {
		names[i] = s.Name
	}
	return provider.Tool{Name: "skill", Description: "Load a skill's instructions, or read one of its files.",
		Schema: json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"name":{"type":"string","enum":%s},"file":{"type":"string","description":"A file of the skill, relative to its folder; empty for SKILL.md"}},"required":["name"],"additionalProperties":false}`,
			mustJSON(names)))}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (t skillTool) Prepare(_ tools.Env, input json.RawMessage) (tools.Plan, error) {
	var in struct{ Name, File string }
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Plan{}, fmt.Errorf("INVALID_JSON: %v", err)
	}
	var sk *Skill
	for i := range t.all {
		if t.all[i].Name == in.Name {
			sk = &t.all[i]
		}
	}
	if sk == nil {
		return tools.Plan{}, fmt.Errorf("no skill named %q", in.Name)
	}
	return tools.Plan{Category: tools.CatRead, Summary: "Load skill " + in.Name + " " + in.File,
		Run: func(context.Context) tools.Result {
			if in.File == "" {
				files := sk.Files()
				text := sk.Body
				if len(files) > 0 {
					text += "\n\nFiles in this skill (folder " + sk.Dir + "):\n- " + strings.Join(files, "\n- ")
				}
				return tools.Result{Text: text}
			}
			p := filepath.Join(sk.Dir, filepath.FromSlash(in.File))
			if rel, err := filepath.Rel(sk.Dir, p); err != nil || strings.HasPrefix(rel, "..") {
				return tools.Result{Text: "the file must be inside the skill's folder", IsError: true}
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return tools.Result{Text: err.Error(), IsError: true}
			}
			return tools.Result{Text: string(data)}
		}}, nil
}
