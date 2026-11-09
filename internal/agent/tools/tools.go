// Package tools holds the agent's built-in tools. A tool turns its input
// into a Plan: what it would do, described for the approval card, and either
// file edits for the harness to apply or a function to run. Nothing touches
// the workspace before the plan is approved.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/search"
)

// Category groups tools for approval policies: a mode's approval map may
// name a tool or one of these.
const (
	CatRead    = "read"
	CatEdit    = "edit"
	CatDelete  = "delete"
	CatShell   = "shell"
	CatGitPush = "git_push" // and other destructive git commands
	CatNetwork = "network"
	CatMeta    = "meta"    // todo, ask_user: talk to the user, change nothing
	CatCommand = "command" // command tools the user defined in the config
)

// Env is the workspace as tools see it. The app implements it on top of
// the editor buffers, search and language servers.
type Env interface {
	Root() string
	// ReadFile returns an open buffer's text, or the file on disk.
	ReadFile(path string) ([]byte, error)
	// Search runs a project search with the editor's engine and filters.
	Search(ctx context.Context, q search.Query, max int) ([]search.FileResult, error)
	// Diagnostics lists language-server problems, for one file or all open.
	Diagnostics(path string) []string
}

// FileEdit is a proposed change to one file. Old is nil for a new file;
// New is nil to delete the file.
type FileEdit struct {
	Path string
	Old  []byte
	New  []byte
}

// Plan is what a tool call will do once approved.
type Plan struct {
	Category string
	// Summary is the exact command, or what changes, for the approval card.
	Summary string
	// Paths are the files touched; any outside the workspace needs approval.
	Paths []string
	// Edits are applied by the harness, which may apply only some hunks.
	Edits []FileEdit
	// Run does the work for tools that are not file edits.
	Run func(ctx context.Context) Result
	// GrantKey scopes "allow for this session" (a shell command's first
	// word, say); empty uses the tool name.
	GrantKey string
	// Question, for ask_user, is put to the user; the answer is the result.
	Question string
	// Command is the command line a shell or command tool runs, for the
	// guard's command rules.
	Command string
	// Hosts are the network hosts a call reaches, for the guard.
	Hosts []string
}

// Result is what goes back to the model.
type Result struct {
	Text    string
	IsError bool
	// Images go back to the model with the text, such as a picture read.
	Images []provider.ImageData
}

// Tool is one capability offered to the model.
type Tool interface {
	Spec() provider.Tool
	Prepare(env Env, input json.RawMessage) (Plan, error)
}

// Registry is the set of tools, by name.
type Registry struct {
	byName map[string]Tool
	order  []string
}

// NewRegistry holds tools in the order given.
func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{byName: map[string]Tool{}}
	for _, t := range ts {
		r.Add(t)
	}
	return r
}

// Add registers t, replacing a tool of the same name.
func (r *Registry) Add(t Tool) {
	name := t.Spec().Name
	if _, ok := r.byName[name]; !ok {
		r.order = append(r.order, name)
	}
	r.byName[name] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Names lists the tools in registration order.
func (r *Registry) Names() []string { return append([]string(nil), r.order...) }

// Filter keeps the tools whose names match patterns ("all", exact names,
// or globs such as "lsp_*"). A mode enforces its limits by the tools it
// offers, not just by asking nicely.
func (r *Registry) Filter(patterns []string) *Registry {
	out := NewRegistry()
	for _, name := range r.order {
		for _, p := range patterns {
			if p == "all" || p == name {
				out.Add(r.byName[name])
				break
			}
			if ok, _ := filepath.Match(p, name); ok {
				out.Add(r.byName[name])
				break
			}
		}
	}
	return out
}

// Specs returns the tool definitions for a request, sorted by name so the
// cached prompt prefix stays byte-identical.
func (r *Registry) Specs() []provider.Tool {
	out := make([]provider.Tool, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n].Spec())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Builtin returns the built-in tools. shells runs background commands for
// the shell tools; todos backs the todo tool.
func Builtin(shells *Shells, todos *Todos) []Tool {
	return []Tool{
		readTool{}, writeTool{}, editTool{}, multiEditTool{}, deleteTool{},
		lsTool{}, globTool{}, grepTool{}, searchTool{},
		&shellTool{shells: shells}, &shellOutputTool{shells: shells},
		gitTool{name: "git_status"}, gitTool{name: "git_diff"}, gitTool{name: "git_log"},
		diagnosticsTool{}, &todoTool{todos: todos}, askTool{},
	}
}

// --- helpers ----------------------------------------------------------------

// schema builds a JSON Schema object from properties and required names.
func schema(props map[string]any, required ...string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	b, _ := json.Marshal(map[string]any{
		"type": "object", "properties": props, "required": required, "additionalProperties": false,
	})
	return b
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// decode parses input strictly: unknown fields are an error, so a
// truncated or confused call is caught before it runs.
func decode(input json.RawMessage, v any) error {
	if !json.Valid(input) {
		return errors.New("INVALID_JSON: the tool input is not valid JSON (it may have been cut off); send the call again")
	}
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}
	return nil
}

// resolve makes path absolute against the workspace root.
func resolve(env Env, path string) string {
	if path == "" {
		return env.Root()
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(env.Root(), path)
	}
	return filepath.Clean(path)
}

// Inside reports whether path is inside root.
func Inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// rel shows path relative to the root when inside it.
func rel(env Env, path string) string {
	if r, err := filepath.Rel(env.Root(), path); err == nil && Inside(env.Root(), path) {
		return filepath.ToSlash(r)
	}
	return path
}

func ok(format string, args ...any) Result { return Result{Text: fmt.Sprintf(format, args...)} }
func fail(format string, args ...any) Result {
	return Result{Text: fmt.Sprintf(format, args...), IsError: true}
}

// clip keeps tool output to a size the model can use.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	head := s[:max*3/4]
	tail := s[len(s)-max/4:]
	return head + fmt.Sprintf("\n… %d bytes cut …\n", len(s)-max) + tail
}
