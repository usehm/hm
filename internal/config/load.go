// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed defaults.yaml
var defaultsYAML []byte

// DefaultsYAML returns the built-in defaults with their comments.
func DefaultsYAML() []byte { return bytes.Clone(defaultsYAML) }

// Problem is one thing wrong with a config file.
type Problem struct {
	File      string // path, or "defaults"
	Line, Col int    // 1-based; 0 when unknown
	Key       string // dotted key path, when known
	Msg       string
}

func (p Problem) Error() string {
	var b strings.Builder
	b.WriteString(p.File)
	if p.Line > 0 {
		fmt.Fprintf(&b, ":%d:%d", p.Line, p.Col)
	}
	b.WriteString(": ")
	if p.Key != "" {
		b.WriteString(p.Key + ": ")
	}
	b.WriteString(p.Msg)
	return b.String()
}

// Layer is one config source.
type Layer struct {
	Name string // path shown in problems
	Data []byte
	// Under nests the file's mapping under these keys, as a language file
	// (languages/rust.yaml) holds just languages.rust.
	Under []string
}

// LanguagesDir is the folder of per-language files next to a config file:
// each languages/<name>.yaml holds what languages.<name> would.
func LanguagesDir(configFile string) string {
	return filepath.Join(filepath.Dir(configFile), "languages")
}

// languageLayers reads the language files in dir, in name order.
func languageLayers(dir string) ([]Layer, []Problem) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []Problem{{File: dir, Msg: err.Error()}}
	}
	var layers []Layer
	var problems []Problem
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		l, err := ReadLayer(path)
		if err != nil {
			problems = append(problems, Problem{File: path, Msg: err.Error()})
			continue
		}
		l.Under = []string{"languages", strings.TrimSuffix(e.Name(), ext)}
		layers = append(layers, l)
	}
	return layers, problems
}

// ReadLayer reads path; a missing file is an empty layer.
func ReadLayer(path string) (Layer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Layer{Name: path}, nil
	}
	return Layer{Name: path, Data: data}, err
}

// Load merges the defaults and layers in order. A layer with problems is left
// out of the result, so the returned Config is always usable; callers that
// must not apply a partly broken set check len(problems).
func Load(layers ...Layer) (Config, []Problem) {
	all := append([]Layer{{Name: "defaults", Data: defaultsYAML}}, layers...)
	var merged *yaml.Node
	var problems []Problem
	for _, l := range all {
		root, ps := parseLayer(l)
		if len(ps) > 0 {
			problems = append(problems, ps...)
			continue
		}
		if root == nil {
			continue
		}
		if merged == nil {
			merged = root
		} else {
			mergeNodes(merged, root)
		}
	}
	var cfg Config
	if merged != nil {
		if err := merged.Decode(&cfg); err != nil {
			problems = append(problems, Problem{File: "merged", Msg: err.Error()})
		}
	}
	return cfg, problems
}

// parseLayer returns the layer's top mapping, or nil for an empty file.
func parseLayer(l Layer) (*yaml.Node, []Problem) {
	if len(bytes.TrimSpace(l.Data)) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(l.Data))
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, []Problem{syntaxProblem(l.Name, err)}
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, []Problem{{File: l.Name, Line: root.Line, Col: root.Column, Msg: "the file must be a mapping of settings"}}
	}
	for i := len(l.Under) - 1; i >= 0; i-- {
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: l.Under[i]}
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{key, root}}
	}
	v := validator{file: l.Name}
	v.walk(root, reflect.TypeOf(Config{}), nil)
	v.rules(root)
	return root, v.problems
}

// syntaxProblem turns a yaml.v3 parse error ("yaml: line 3: ...") into a
// positioned problem.
func syntaxProblem(file string, err error) Problem {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	p := Problem{File: file, Msg: msg}
	var line int
	if n, _ := fmt.Sscanf(msg, "line %d:", &line); n == 1 {
		p.Line, p.Col = line, 1
		if i := strings.Index(msg, ": "); i >= 0 {
			p.Msg = msg[i+2:]
		}
	}
	return p
}

// mergeNodes merges mapping src into mapping dst: nested mappings merge key
// by key, anything else is replaced.
func mergeNodes(dst, src *yaml.Node) {
	for i := 0; i+1 < len(src.Content); i += 2 {
		k, v := src.Content[i], src.Content[i+1]
		j := mappingIndex(dst, k.Value)
		if j < 0 {
			dst.Content = append(dst.Content, k, v)
			continue
		}
		cur := dst.Content[j+1]
		if cur.Kind == yaml.MappingNode && v.Kind == yaml.MappingNode {
			mergeNodes(cur, v)
		} else {
			dst.Content[j+1] = v
		}
	}
}

// mappingIndex returns the index of key's key node in mapping m, or -1.
func mappingIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// UserPath is the user config file: $XDG_CONFIG_HOME/hm/config.yaml or
// ~/.config/hm/config.yaml on every OS, so dotfiles repos can carry it.
// StateDir is where hm keeps app state: sessions, projects, window state.
// Tests point it elsewhere.
var StateDir = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "hm")
}

func UserPath() string { return filepath.Join(UserDir(), "config.yaml") }

// UserDir is the user config folder. Tests point it elsewhere.
var UserDir = func() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "hm")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "hm")
	}
	return filepath.Join(home, ".config", "hm")
}

// ProjectPath is the project config file under root.
func ProjectPath(root string) string { return filepath.Join(root, ".hm", "config.yaml") }

// ModelsFile is the model registry kept beside each config file: it adds
// models to hm's table or changes their limits and prices.
const ModelsFile = "models.json"

// ModelsPaths are the registry files for a config's user and project
// files, the project's last so it wins; an empty path is left out.
func ModelsPaths(user, project string) []string {
	var out []string
	for _, f := range []string{user, project} {
		if f != "" {
			out = append(out, filepath.Join(filepath.Dir(f), ModelsFile))
		}
	}
	return out
}

// ExpandHome replaces a leading ~ with the home folder.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
