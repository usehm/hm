package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

func toolNames(req map[string]any) []string {
	var out []string
	for _, t := range req["tools"].([]any) {
		out = append(out, fmt.Sprint(t.(map[string]any)["function"].(map[string]any)["name"]))
	}
	return out
}

func hasTool(req map[string]any, name string) bool {
	for _, n := range toolNames(req) {
		if n == name {
			return true
		}
	}
	return false
}

// The task tool runs a read-only sub-agent in a session of its own and
// gives its report back; two tasks in one message run side by side.
func TestTaskToolRunsSubagents(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{
		{{"task", `{"description":"look at main","prompt":"Read main.go and report"}`},
			{"task", `{"description":"look at util","prompt":"Read util and report"}`}},
		nil, nil, nil,
	}}
	h, _ := agentProject(t, "openai", model)
	runTask(t, h, "survey the code")
	r := h.app.run()
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	if r.errText != "" {
		t.Fatal(r.errText)
	}
	if len(model.requests) != 4 {
		t.Fatalf("requests %d", len(model.requests))
	}
	if !hasTool(model.requests[0], "task") {
		t.Fatal("the parent should be offered task")
	}
	for _, req := range model.requests[1:3] {
		if hasTool(req, "task") || hasTool(req, "edit") || hasTool(req, "shell") {
			t.Fatalf("a read-only sub-agent at the depth limit got %v", toolNames(req))
		}
		if !strings.Contains(fmt.Sprint(req["messages"]), "You are a sub-agent") {
			t.Fatal("the sub-agent prompt is missing")
		}
	}
	results := 0
	for _, m := range r.session.Snapshot() {
		for _, b := range m.Blocks {
			if b.Type == provider.ToolResult && b.ToolName == "task" {
				results++
				if b.IsError || !strings.Contains(b.Text, "Done: main prints 1.") || !strings.Contains(b.Text, "(sub-agent session ") {
					t.Fatalf("task result %+v", b)
				}
			}
		}
	}
	if results != 2 || len(r.children) != 2 {
		t.Fatalf("results %d children %d", results, len(r.children))
	}
	h.frame() // the sub-agent rows render
	kids := 0
	for _, s := range h.app.sessionList() {
		if s.ParentID == r.session.ID {
			kids++
		}
	}
	if kids != 2 {
		t.Fatalf("saved sub-agent sessions %d", kids)
	}
	for _, ch := range r.children {
		h.app.resumeAgentSession(ch.session.ID)
		if h.app.run().session.ID != ch.session.ID {
			t.Fatal("opening a sub-agent should show its run")
		}
		break
	}
}

// A background task returns at once; its report arrives later as a
// message and wakes the idle parent.
func TestBackgroundTaskReportsByMessage(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{
		{{"task", `{"description":"slow look","prompt":"Report on main","background":true}`}},
	}}
	h, _ := agentProject(t, "openai", model)
	runTask(t, h, "start it")
	r := h.app.run()
	h.until("report", 10*time.Second, func() bool {
		if r.running {
			return false
		}
		return strings.Contains(fmt.Sprint(r.session.Snapshot()), "Report of the background task")
	})
	h.until("parent idle", 10*time.Second, func() bool { return !r.running })
}
