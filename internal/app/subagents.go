package app

import (
	"context"
	"time"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/host"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// agentLink is how one run's tools reach other agents.
type agentLink struct {
	a *App
	r *agentRun
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
			_ = a.agent.bus.Send(id, parent.session.ID, host.BackgroundReport(t, report), true)
		}()
		return host.BackgroundStarted(id, t)
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
	return host.Report(ctx, child.session, err)
}

// newChildRun makes the run of a sub-agent of parent, started by the task
// call callID.
func (a *App) newChildRun(parent *agentRun, callID string, t tools.Task) *agentRun {
	s := host.NewSubagentSession(parent.session, parent.mode, parent.provider, t)
	s.Root = a.ws.Root
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

func (l agentLink) Send(to, text string, followUp bool) error {
	return l.a.agent.bus.Send(l.r.session.ID, to, text, followUp)
}

func (l agentLink) Wait(ctx context.Context, from string, timeout time.Duration) (string, string, bool) {
	m, ok := l.a.agent.bus.Wait(ctx, l.r.session.ID, from, timeout)
	if !ok {
		return "", "", false
	}
	return host.Sender(m), m.Text, true
}

func (l agentLink) Peers() []tools.Peer {
	return host.Peers(l.a.agent.bus, l.r.session.ID)
}
