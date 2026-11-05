package config

import (
	"encoding"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"
	"gopkg.in/yaml.v3"
)

type validator struct {
	file     string
	problems []Problem
}

func (v *validator) add(n *yaml.Node, path []string, format string, args ...any) {
	v.problems = append(v.problems, Problem{
		File: v.file, Line: n.Line, Col: n.Column,
		Key: strings.Join(path, "."), Msg: fmt.Sprintf(format, args...),
	})
}

var (
	textUnmarshaler = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	yamlUnmarshaler = reflect.TypeOf((*yaml.Unmarshaler)(nil)).Elem()
)

// walk checks n against the Go type t: unknown keys, wrong shapes and values
// that do not decode, each with the node's position.
func (v *validator) walk(n *yaml.Node, t reflect.Type, path []string) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if isNull(n) {
		return
	}
	pt := reflect.PointerTo(t)
	if pt.Implements(textUnmarshaler) || pt.Implements(yamlUnmarshaler) {
		if err := n.Decode(reflect.New(t).Interface()); err != nil {
			v.add(n, path, "%s", cleanDecodeErr(err))
		}
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		if n.Kind != yaml.MappingNode {
			v.add(n, path, "expected a mapping of settings")
			return
		}
		fields := yamlFields(t)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, val := n.Content[i], n.Content[i+1]
			f, ok := fields[k.Value]
			if !ok {
				msg := "unknown key " + quote(k.Value)
				if s := closest(k.Value, keysOf(fields)); s != "" {
					msg += fmt.Sprintf(", did you mean %s?", quote(s))
				}
				v.add(k, append(path, k.Value), "%s", msg)
				continue
			}
			v.walk(val, f, append(path, k.Value))
		}
	case reflect.Map:
		if n.Kind != yaml.MappingNode {
			v.add(n, path, "expected a mapping")
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			v.walk(n.Content[i+1], t.Elem(), append(path, n.Content[i].Value))
		}
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode {
			v.add(n, path, "expected a list")
			return
		}
		for i, item := range n.Content {
			v.walk(item, t.Elem(), append(path, fmt.Sprint(i)))
		}
	case reflect.Interface:
	default:
		if n.Kind != yaml.ScalarNode {
			v.add(n, path, "expected %s", typeName(t))
			return
		}
		if err := n.Decode(reflect.New(t).Interface()); err != nil {
			v.add(n, path, "expected %s, got %s", typeName(t), quote(n.Value))
		}
	}
}

func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

func yamlFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		out[name] = f.Type
	}
	return out
}

func keysOf(m map[string]reflect.Type) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func typeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int64, reflect.Int32:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.String:
		return "text"
	}
	return t.String()
}

func cleanDecodeErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && strings.Contains(s, "line ") {
		s = s[i+2:]
	}
	return strings.TrimPrefix(s, "yaml: ")
}

func quote(s string) string { return "\"" + s + "\"" }

// closest suggests the candidate within two edits of s.
func closest(s string, cands []string) string {
	best, bestD := "", 3
	for _, c := range cands {
		if d := editDistance(s, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// rule checks the value at a key path; "*" matches any map key.
type rule struct {
	path  string
	check func(n *yaml.Node) string
}

// Enums lists the allowed words per key path; the JSON schema uses it too.
var Enums = map[string][]string{
	"editor.auto_save":               {AutoSaveOff, AutoSaveFocusChange, AutoSaveDelay},
	"search.engine":                  {"auto", "ripgrep", "builtin"},
	"updates.channel":                {"stable", "prerelease", "off"},
	"agent.layout":                   {"fullscreen", "panel"},
	"artifacts.storage":              {"none", "gcs", "s3", "git"},
	"editor.keymap":                  {"default", "vim"},
	"editor.auto_indent":             {AutoIndentNone, AutoIndentKeep, AutoIndentAdvanced},
	"languages.*.editor.auto_indent": {AutoIndentNone, AutoIndentKeep, AutoIndentAdvanced},
	"agent.providers.*.type":         {"anthropic", "openai"},
	"agent.providers.*.effort":       {"low", "medium", "high", "xhigh", "max"},
	"modes.*.approval.*":             {"auto", "ask", "deny"},
	"tools.mcp.*.transport":          {"stdio", "http"},
	"tools.mcp.*.approval":           {"auto", "ask", "deny"},
	"tools.commands.*.approval":      {"auto", "ask", "deny"},
	"tools.web.search.provider":      {"duckduckgo", "searxng", "brave", "tavily", "exa"},
	"languages.*.formatters.*.on.*":  FormatTriggers,
	"tools.commands.*.params.*.type": {"string", "number", "integer", "boolean"},
}

var rules = func() []rule {
	rs := []rule{
		{"editor.theme", func(n *yaml.Node) string {
			if n.Value == "system" || slices.Contains(theme.Names(), n.Value) {
				return ""
			}
			msg := "unknown theme " + quote(n.Value)
			if s := closest(n.Value, theme.Names()); s != "" {
				msg += ", did you mean " + quote(s) + "?"
			}
			return msg
		}},
		{"editor.font_size", intRange(6, 72)},
		{"editor.zoom", floatRange(0.5, 3)},
		{"editor.tab_width", intRange(1, 16)},
		{"languages.*.editor.tab_width", intRange(1, 16)},
		{"terminal.font_size", intRange(6, 72)},
		{"search.max_results", intRange(1, 1000000)},
		{"agent.context.max_bytes", intRange(1, 16<<20)},
		{"agent.context.files.*", func(n *yaml.Node) string {
			if msg := nonEmpty(n); msg != "" {
				return msg
			}
			if strings.ContainsAny(n.Value, `/\`) {
				return "must be a file name, not a path"
			}
			return ""
		}},
		{"agent.prompts.paths.*", nonEmpty},
		{"keys.*", shortcutRule},
		{"keybindings.*.key", shortcutRule},
		{"keybindings.*.command", nonEmpty},
		{"keybindings.*.when", func(n *yaml.Node) string {
			if _, err := ParseWhen(n.Value); err != nil {
				return "bad condition: " + err.Error()
			}
			return ""
		}},
		{"languages.*.formatters.*.command", nonEmpty},
		{"languages.*.lsp.command", nonEmpty},
		{"tools.commands.*.command", nonEmpty},
		{"tools.commands.*.description", nonEmpty},
	}
	for path, words := range Enums {
		rs = append(rs, rule{path, oneOf(words)})
	}
	return rs
}()

func intRange(lo, hi int) func(*yaml.Node) string {
	return func(n *yaml.Node) string {
		var v int
		if n.Decode(&v) != nil {
			return "" // walk reports the type error
		}
		if v < lo || v > hi {
			return fmt.Sprintf("must be between %d and %d", lo, hi)
		}
		return ""
	}
}

func floatRange(lo, hi float64) func(*yaml.Node) string {
	return func(n *yaml.Node) string {
		var v float64
		if n.Decode(&v) != nil {
			return "" // walk reports the type error
		}
		if v < lo || v > hi {
			return fmt.Sprintf("must be between %g and %g", lo, hi)
		}
		return ""
	}
}

func oneOf(words []string) func(*yaml.Node) string {
	return func(n *yaml.Node) string {
		if n.Kind != yaml.ScalarNode || slices.Contains(words, n.Value) {
			return ""
		}
		return fmt.Sprintf("must be one of %s, got %s", strings.Join(words, ", "), quote(n.Value))
	}
}

func nonEmpty(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode && strings.TrimSpace(n.Value) == "" {
		return "must not be empty"
	}
	return ""
}

func (v *validator) rules(root *yaml.Node) {
	for _, r := range rules {
		match(root, strings.Split(r.path, "."), nil, func(n *yaml.Node, path []string) {
			if isNull(n) {
				return
			}
			if msg := r.check(n); msg != "" {
				v.add(n, path, "%s", msg)
			}
		})
	}
}

// match calls fn for every node under n at pattern.
func match(n *yaml.Node, pattern, path []string, fn func(*yaml.Node, []string)) {
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if len(pattern) == 0 {
		fn(n, path)
		return
	}
	p := pattern[0]
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i].Value; p == "*" || p == k {
				match(n.Content[i+1], pattern[1:], append(path, k), fn)
			}
		}
	case yaml.SequenceNode:
		if p != "*" {
			return
		}
		for i, item := range n.Content {
			match(item, pattern[1:], append(path, fmt.Sprint(i)), fn)
		}
	}
}

func shortcutRule(n *yaml.Node) string {
	if n.Value == "" {
		return "" // no shortcut
	}
	if _, err := ParseShortcut(n.Value); err != nil {
		return err.Error()
	}
	return ""
}

// ParseShortcut parses a config shortcut: a chord such as "cmd+shift+p",
// "ctrl+`" or "alt+mouse4", or two in a row such as "cmd+k cmd+s".
func ParseShortcut(s string) (ui.KeySeq, error) {
	seq, err := ui.ParseKeySeq(s)
	if err != nil {
		return seq, fmt.Errorf("bad shortcut %s: %v", quote(s), err)
	}
	return seq, nil
}
