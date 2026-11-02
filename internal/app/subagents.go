package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// agentLink is how one run's tools reach other agents.
type agentLink struct {
	a *App
	r *agentRun
}

// subagentPrompt tells a sub-agent what it is.
func subagentPrompt(r *agentRun) string {
	access := "You can read but not edit files or run commands."
	if r.write {
		access = "You may edit files and run commands; the user approves them as for any agent."
	}
	return "# Sub-agent\nYou are a sub-agent: another agent handed you one task. " + access +
		" Work on the task alone; the user does not see your messages, only your last one goes back to the " +
		"agent that started you. End with a complete, self-contained report of what you found or did. " +
		"Your parent is reachable as \"parent\" with send_message."
}

// Spawn runs a task in a sub-agent's session: in the background it returns
// at once and the report reaches the parent as a message.
func (l agentLink) Spawn(ctx context.Context, callID string, t tools.Task) tools.Result {
	a, parent := l.a, l.r
	var child *agentRun
	var ag *agent.Agent
	var err error
	if cerr := a.callUI(func() {
		child = a.newChildRun(parent, callID, t)
		ag, err = a.agentFor(child)
	}); cerr != nil {
		return tools.Result{Text: cerr.Error(), IsError: true}
	}
	if err != nil {
		return tools.Result{Text: "Could not start the sub-agent: " + err.Error(), IsError: true}
	}
	id := child.session.ID
	if t.Background {
		go func() {
			report := l.work(context.Background(), child, ag, t.Prompt)
			_ = a.agent.bus.Send(id, parent.session.ID, "Report of the background task \""+t.Description+"\":\n"+report.Text)
		}()
		return tools.Result{Text: fmt.Sprintf("Started sub-agent %s (%q) in the background. Its report will arrive as a message; "+
			"wait_message from %s waits for it.", id, t.Description, id)}
	}
	return l.work(ctx, child, ag, t.Prompt)
}

// work runs the sub-agent's turn and returns its last message as the
// report.
func (l agentLink) work(ctx context.Context, child *agentRun, ag *agent.Agent, prompt string) tools.Result {
	a, parent := l.a, l.r
	select {
	case parent.subSem <- struct{}{}:
	case <-ctx.Done():
		return tools.Result{Text: "Cancelled", IsError: true}
	}
	defer func() { <-parent.subSem }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_ = a.callUI(func() {
		child.running, child.cancel = true, cancel
		child.session.SetStatus(agent.StatusRunning)
		a.agent.bus.SetRunning(child.session.ID, true)
	})
	err := ag.Run(ctx, child.session, prompt)
	done := make(chan struct{})
	a.onUI(func() {
		a.turnEnded(child, err)
		a.untrack(child)
		close(done)
	})
	select {
	case <-done:
	case <-time.After(uiCallTimeout):
	}
	report := lastAssistantText(child.session.Snapshot())
	switch {
	case err != nil && ctx.Err() != nil:
		return tools.Result{Text: "The sub-agent was cancelled. Its last words: " + report, IsError: true}
	case err != nil:
		return tools.Result{Text: "The sub-agent failed: " + err.Error() + "\n" + report, IsError: true}
	case report == "":
		return tools.Result{Text: "The sub-agent finished without a report", IsError: true}
	}
	return tools.Result{Text: report + fmt.Sprintf("\n\n(sub-agent session %s)", child.session.ID)}
}

func lastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.Assistant {
			if t := strings.TrimSpace(msgs[i].TextOf()); t != "" {
				return t
			}
		}
	}
	return ""
}

// newChildRun makes the run of a sub-agent of parent, started by the task
// call callID.
func (a *App) newChildRun(parent *agentRun, callID string, t tools.Task) *agentRun {
	s := agent.NewSession(parent.mode, parent.provider)
	s.Root, s.ProjectID, s.ParentID = a.ws.Root, parent.session.ProjectID, parent.session.ID
	s.Title = t.Description
	child := a.newRun(s)
	child.parent, child.depth, child.write = parent, parent.depth+1, t.Write
	child.mode, child.provider = parent.mode, parent.provider
	child.focusInput = false
	if parent.children == nil {
		parent.children = map[string]*agentRun{}
	}
	parent.children[callID] = child
	a.track(child)
	_ = a.agent.store.Save(s)
	a.agent.sessionsStale = true
	return child
}

func (l agentLink) Send(to, text string) error {
	return l.a.agent.bus.Send(l.r.session.ID, to, text)
}

func (l agentLink) Wait(ctx context.Context, from string, timeout time.Duration) (string, string, bool) {
	m, ok := l.a.agent.bus.Wait(ctx, l.r.session.ID, from, timeout)
	if !ok {
		return "", "", false
	}
	sender := m.From
	if m.FromName != "" {
		sender += " (" + m.FromName + ")"
	}
	return sender, m.Text, true
}

func (l agentLink) Peers() []tools.Peer {
	var out []tools.Peer
	for _, p := range l.a.agent.bus.Peers(l.r.session.ID) {
		out = append(out, tools.Peer{ID: p.ID, Name: p.Name, Role: p.Role, Running: p.Running})
	}
	return out
}
