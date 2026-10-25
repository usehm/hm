package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

// --- todo -------------------------------------------------------------------

// Todo is one task in the agent's plan.
type Todo struct {
	Text   string `json:"text"`
	Status string `json:"status"` // pending | in_progress | done
}

// Todos is the task list the panel shows.
type Todos struct {
	mu    sync.Mutex
	items []Todo
}

// Items returns a copy of the list.
func (t *Todos) Items() []Todo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Todo(nil), t.items...)
}

// Set replaces the list.
func (t *Todos) Set(items []Todo) {
	t.mu.Lock()
	t.items = items
	t.mu.Unlock()
}

type todoTool struct{ todos *Todos }

func (*todoTool) Spec() provider.Tool {
	return provider.Tool{Name: "todo", Description: "Keep a short task list for multi-step work; the user sees it. " +
		"Send the whole list each time, with one task in_progress.",
		Schema: schema(map[string]any{
			"items": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":   str("The task"),
					"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done"}},
				},
				"required": []string{"text", "status"}, "additionalProperties": false,
			}},
		}, "items")}
}

func (t *todoTool) Prepare(_ Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Items []Todo `json:"items"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	for _, it := range in.Items {
		switch it.Status {
		case "pending", "in_progress", "done":
		default:
			return Plan{}, fmt.Errorf("status %q: use pending, in_progress or done", it.Status)
		}
	}
	return Plan{Category: CatMeta, Summary: "Update the task list", Run: func(context.Context) Result {
		t.todos.Set(in.Items)
		done := 0
		for _, it := range in.Items {
			if it.Status == "done" {
				done++
			}
		}
		return ok("Task list updated: %d of %d done", done, len(in.Items))
	}}, nil
}

// --- ask_user ---------------------------------------------------------------

type askTool struct{}

func (askTool) Spec() provider.Tool {
	return provider.Tool{Name: "ask_user", Description: "Ask the user a question and wait for the answer. " +
		"Use it when a decision is theirs to make; don't ask what you can find out.",
		Schema: schema(map[string]any{"question": str("The question, with the options if there are any")}, "question")}
}

func (askTool) Prepare(_ Env, input json.RawMessage) (Plan, error) {
	var in struct{ Question string }
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(in.Question) == "" {
		return Plan{}, errors.New("question is empty")
	}
	return Plan{Category: CatMeta, Summary: in.Question, Question: in.Question}, nil
}

// --- lsp_diagnostics --------------------------------------------------------

type diagnosticsTool struct{}

func (diagnosticsTool) Spec() provider.Tool {
	return provider.Tool{Name: "lsp_diagnostics", Description: "List compiler and linter problems from the " +
		"editor's language servers, for one file or every open file.",
		Schema: schema(map[string]any{"path": str("A file; empty for every open file")})}
}

func (diagnosticsTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in struct{ Path string }
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	path := ""
	if in.Path != "" {
		path = resolve(env, in.Path)
	}
	return Plan{Category: CatRead, Summary: "Read diagnostics", Run: func(context.Context) Result {
		ds := env.Diagnostics(path)
		if len(ds) == 0 {
			return ok("No problems reported")
		}
		return ok("%s", clip(strings.Join(ds, "\n"), 30000))
	}}, nil
}
