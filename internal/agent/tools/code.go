package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/usehm/hm/internal/agent/provider"
)

// Location is a place in a file; Line and Col are 1-based.
type Location struct {
	Path      string
	Line, Col int
	Text      string // the line, for context
}

// Code is the editor's language intelligence: the same language servers
// and formatter chains the editor uses.
type Code interface {
	Definition(ctx context.Context, path string, line, col int) ([]Location, error)
	References(ctx context.Context, path string, line, col int) ([]Location, error)
	Hover(ctx context.Context, path string, line, col int) (string, error)
	Rename(ctx context.Context, path string, line, col int, newName string) ([]FileEdit, error)
	// Format returns the file formatted with its language's formatter
	// chain, or the language server's formatting.
	Format(ctx context.Context, path string) ([]byte, error)
}

// CodeTools returns the lsp_* and format tools backed by code.
func CodeTools(code Code) []Tool {
	return []Tool{
		&lspTool{name: "lsp_definition", code: code}, &lspTool{name: "lsp_references", code: code},
		&lspTool{name: "lsp_hover", code: code}, &renameTool{code: code}, &formatTool{code: code},
	}
}

var posProps = map[string]any{
	"path":   str("File path"),
	"line":   integer("Line, 1-based"),
	"column": integer("Column of the symbol, 1-based, in characters"),
}

type posInput struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

func (p posInput) check() error {
	if p.Line < 1 || p.Column < 1 {
		return errors.New("line and column are 1-based")
	}
	return nil
}

type lspTool struct {
	name string
	code Code
}

func (t *lspTool) Spec() provider.Tool {
	desc := map[string]string{
		"lsp_definition": "Find where the symbol at a position is defined, using the language server.",
		"lsp_references": "Find every use of the symbol at a position, using the language server.",
		"lsp_hover":      "Show the type and documentation of the symbol at a position.",
	}[t.name]
	return provider.Tool{Name: t.name, Description: desc, Schema: schema(posProps, "path", "line", "column")}
}

func (t *lspTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in posInput
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	if err := in.check(); err != nil {
		return Plan{}, err
	}
	path := resolve(env, in.Path)
	summary := fmt.Sprintf("%s %s:%d:%d", strings.TrimPrefix(t.name, "lsp_"), rel(env, path), in.Line, in.Column)
	return Plan{Category: CatRead, Summary: summary, Paths: []string{path}, Run: func(ctx context.Context) Result {
		if t.name == "lsp_hover" {
			text, err := t.code.Hover(ctx, path, in.Line, in.Column)
			if err != nil {
				return fail("%v", err)
			}
			if strings.TrimSpace(text) == "" {
				return ok("No information at that position")
			}
			return ok("%s", clip(text, 20000))
		}
		var locs []Location
		var err error
		if t.name == "lsp_definition" {
			locs, err = t.code.Definition(ctx, path, in.Line, in.Column)
		} else {
			locs, err = t.code.References(ctx, path, in.Line, in.Column)
		}
		if err != nil {
			return fail("%v", err)
		}
		if len(locs) == 0 {
			return ok("Nothing found")
		}
		var b strings.Builder
		for _, l := range locs {
			fmt.Fprintf(&b, "%s:%d:%d: %s\n", rel(env, l.Path), l.Line, l.Col, strings.TrimSpace(l.Text))
		}
		return ok("%s", clip(b.String(), 30000))
	}}, nil
}

type renameTool struct{ code Code }

func (*renameTool) Spec() provider.Tool {
	props := map[string]any{"new_name": str("The new name")}
	for k, v := range posProps {
		props[k] = v
	}
	return provider.Tool{Name: "lsp_rename", Description: "Rename the symbol at a position everywhere it is used, " +
		"through the language server.", Schema: schema(props, "path", "line", "column", "new_name")}
}

func (t *renameTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct {
		posInput
		NewName string `json:"new_name"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	if err := in.check(); err != nil {
		return Plan{}, err
	}
	path := resolve(env, in.Path)
	edits, err := t.code.Rename(context.Background(), path, in.Line, in.Column, in.NewName)
	if err != nil {
		return Plan{}, err
	}
	if len(edits) == 0 {
		return Plan{}, errors.New("the language server found nothing to rename there")
	}
	paths := make([]string, len(edits))
	for i, e := range edits {
		paths[i] = e.Path
	}
	return Plan{Category: CatEdit, Summary: fmt.Sprintf("Rename to %s in %d files", in.NewName, len(edits)),
		Paths: paths, Edits: edits}, nil
}

type formatTool struct{ code Code }

func (*formatTool) Spec() provider.Tool {
	return provider.Tool{Name: "format", Description: "Format a file with the editor's formatter for its language.",
		Schema: schema(map[string]any{"path": str("File path")}, "path")}
}

func (t *formatTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct{ Path string }
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	path := resolve(env, in.Path)
	old, err := env.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	out, err := t.code.Format(context.Background(), path)
	if err != nil {
		return Plan{}, err
	}
	if string(out) == string(old) {
		return Plan{Category: CatRead, Summary: "Format " + rel(env, path), Run: func(context.Context) Result {
			return ok("%s is already formatted", rel(env, path))
		}}, nil
	}
	return Plan{Category: CatEdit, Summary: "Format " + rel(env, path), Paths: []string{path},
		Edits: []FileEdit{{Path: path, Old: old, New: out}}}, nil
}
