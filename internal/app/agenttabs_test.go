package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/provider"
)

// A message sent while the agent works waits in the queue and reaches the
// model with the next tool results.
func TestQueuedMessageReachesRunningAgent(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{{{"shell", `{"command":"echo hi"}`}}, nil}}
	h, _ := agentProject(t, "openai", model)
	runTask(t, h, "say hi")
	r := h.app.run()
	h.until("shell approval", 10*time.Second, func() bool { return r.approval != nil })
	r.input = "also check the tests"
	h.app.sendRun(r)
	if q := r.queued(); len(q) != 1 || q[0].FollowUp || r.input != "" {
		t.Fatalf("queue %+v, input %q", q, r.input)
	}
	h.frame() // the queue renders
	h.app.answerApproval(r, true, false)
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	if len(model.requests) != 2 {
		t.Fatalf("requests %d", len(model.requests))
	}
	if second := fmt.Sprint(model.requests[1]["messages"]); !strings.Contains(second, "also check the tests") {
		t.Fatal("the queued message did not reach the model")
	}
	if len(r.queued()) != 0 {
		t.Fatal("the queue should be empty")
	}
}

// A follow-up sent while the agent works waits for the turn to end, then
// starts another step.
func TestFollowUpWaitsForTurnEnd(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{{{"shell", `{"command":"echo hi"}`}}, nil, nil}}
	h, _ := agentProject(t, "openai", model)
	runTask(t, h, "say hi")
	r := h.app.run()
	h.until("shell approval", 10*time.Second, func() bool { return r.approval != nil })
	r.input = "then update the docs"
	h.app.sendRunAs(r, true)
	h.frame()
	h.app.answerApproval(r, true, false)
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	if len(model.requests) != 3 {
		t.Fatalf("requests %d", len(model.requests))
	}
	if second := fmt.Sprint(model.requests[1]["messages"]); strings.Contains(second, "then update the docs") {
		t.Fatal("the follow-up joined the tool results")
	}
	if third := fmt.Sprint(model.requests[2]["messages"]); !strings.Contains(third, "then update the docs") {
		t.Fatal("the follow-up did not reach the model")
	}
}

// Queued messages keep their kind, and one taken back to edit leaves the
// queue for the message box.
func TestQueuedMessagesEditAndKinds(t *testing.T) {
	r := &agentRun{running: true}
	a := &App{}
	tests := []struct {
		text     string
		followUp bool
	}{{"steer one", false}, {"after one", true}, {"steer two", false}}
	for _, tt := range tests {
		a.sendText(r, agent.Input{Text: tt.text}, tt.followUp)
	}
	q := r.queued()
	if len(q) != 3 || q[1].Text != "after one" || !q[1].FollowUp || q[2].FollowUp {
		t.Fatalf("queue %+v", q)
	}
	r.input = "draft"
	r.editQueued(2)
	if r.input != "draft\n\nsteer two" || !r.focusInput {
		t.Fatalf("input %q", r.input)
	}
	if got := r.takeQueue(false); len(got) != 1 || got[0].Text != "steer one" {
		t.Fatalf("steering %+v", got)
	}
	if got := r.takeQueue(true); len(got) != 1 || got[0].Text != "after one" {
		t.Fatalf("follow-ups %+v", got)
	}
	if len(r.queued()) != 0 {
		t.Fatal("the queue should be empty")
	}
}

// Sessions open in tabs: opening one twice focuses it, tabs cycle and
// close, and they come back after a restart.
func TestAgentTabsOpenFocusAndRestore(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{nil}}
	h, root := agentProject(t, "openai", model)
	runTask(t, h, "first task")
	first := h.app.run()
	h.until("turn end", 10*time.Second, func() bool { return !first.running })
	id := first.session.ID

	h.app.newAgentSession()
	h.app.openAgentPage(agentTabProjects, "")
	h.frame()
	if n := len(h.app.agent.tabs); n != 3 {
		t.Fatalf("tabs %d", n)
	}
	h.app.resumeAgentSession(id)
	if h.app.run() != first || len(h.app.agent.tabs) != 3 {
		t.Fatal("resuming an open session should focus its tab")
	}
	h.app.cycleAgentTab(1)
	if h.app.run() == first {
		t.Fatal("next tab did not move")
	}
	h.app.cycleAgentTab(-1)
	if h.app.run() != first {
		t.Fatal("previous tab did not come back")
	}
	if list := h.app.sessionList(); len(list) != 1 || list[0].Root != root {
		t.Fatalf("sidebar sessions %+v", list)
	}

	tabs, active := h.app.agentTabsState()
	// The new, empty session is not worth reopening.
	if len(tabs) != 2 || tabs[active].Key != id {
		t.Fatalf("saved tabs %+v active %d", tabs, active)
	}
	h.app.restoreAgentTabs(tabs, active)
	if r := h.app.run(); r == nil || r.session.ID != id {
		t.Fatal("restore should focus the saved session")
	}
	h.app.closeAgentTab(h.app.agent.active)
	if h.app.agentTabIndex(agentTabSession, id) >= 0 && len(h.app.agent.tabs) == 0 {
		t.Fatal("close did nothing")
	}
}

// A session saved while running, as when hm crashed, opens interrupted and
// can be resumed.
func TestInterruptedSessionResumes(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{nil}}
	h, root := agentProject(t, "openai", model)
	s := agent.NewSession("auto", "test")
	s.Root, s.Status, s.Title = root, agent.StatusRunning, "cut off"
	s.Append(
		provider.Message{Role: provider.User, Blocks: []provider.Block{{Type: provider.Text, Text: "cut off"}}},
		provider.Message{Role: provider.Assistant, Blocks: []provider.Block{{Type: provider.ToolUse, ToolID: "c1", ToolName: "read", Input: []byte(`{"path":"main.go"}`)}}},
	)
	if err := h.app.agent.store.Save(s); err != nil {
		t.Fatal(err)
	}
	h.app.resumeAgentSession(s.ID)
	r := h.app.run()
	if r == nil || r.session.StatusNow() != agent.StatusInterrupted {
		t.Fatal("the session should open interrupted")
	}
	msgs := r.session.Snapshot()
	if last := msgs[len(msgs)-1]; last.Role != provider.User || last.Blocks[0].ToolID != "c1" {
		t.Fatalf("dangling call not answered: %+v", last)
	}
	h.frame() // the notice renders
	if err := h.app.startTurn(r, agent.Input{Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	if r.session.StatusNow() != agent.StatusIdle {
		t.Fatalf("status %q", r.session.StatusNow())
	}
	saved, _ := h.app.agent.store.Load(s.ID)
	if saved.Status != agent.StatusIdle {
		t.Fatalf("saved status %q", saved.Status)
	}
}

// A project's context reaches the agent of a session started in it.
func TestProjectContextReachesAgent(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{nil}}
	h, _ := agentProject(t, "openai", model)
	p, err := h.app.agent.projects.Put(agent.Project{Title: "Billing", Context: "Amounts are integer cents."})
	if err != nil {
		t.Fatal(err)
	}
	h.app.openAgentPage(agentTabProjects, "")
	h.frame() // the cards render
	h.app.newSessionInProject(p.ID)
	r := h.app.run()
	r.input = "add a fee"
	h.app.sendAgent()
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	if sys := fmt.Sprint(model.requests[0]["messages"]); !strings.Contains(sys, "Amounts are integer cents.") {
		t.Fatal("project context missing from the system prompt")
	}
	// The next new session starts in the same project.
	if h.app.newRun(nil).session.ProjectID != p.ID {
		t.Fatal("a new session should keep the last project")
	}
}
