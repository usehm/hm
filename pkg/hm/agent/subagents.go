package agent

import (
	"context"
	"time"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/host"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// link is how one Runner's tools reach other agents: the sub-agents it
// starts with the task tool and the agents on its bus.
type link struct{ r *Runner }

// Spawn runs a task in a sub-agent's session: in the background it returns
// at once and the report reaches the parent as a message.
func (l link) Spawn(ctx context.Context, _ string, t tools.Task) tools.Result {
	parent := l.r
	child := parent.child(t)
	id := child.session.ID
	if t.Background {
		parent.bg.Add(1)
		go func() {
			defer parent.bg.Add(-1)
			report := child.work(parent.top.background(), t.Prompt)
			_ = parent.bus.Send(id, parent.session.ID, host.BackgroundReport(t, report), true)
		}()
		return host.BackgroundStarted(id, t)
	}
	return child.work(ctx, t.Prompt)
}

// child makes the Runner of a sub-agent of r working on t.
func (r *Runner) child(t tools.Task) *Runner {
	r.mu.Lock()
	mode, prov, model := r.mode, r.provider, r.model
	r.mu.Unlock()
	c := &Runner{opts: r.opts, log: r.log, h: r.h, store: r.store, bus: r.bus, top: r.top, parent: r,
		depth: r.depth + 1, write: t.Write, mode: mode, provider: prov, model: model}
	c.init(host.NewSubagentSession(r.session, mode, prov, t))
	c.save()
	return c
}

// work runs the sub-agent's turn and returns its last message as the
// report. Its mailbox and shells close after.
func (c *Runner) work(ctx context.Context, prompt string) tools.Result {
	parent := c.parent
	defer func() {
		c.bus.Unregister(c.session.ID)
		c.shells.StopAll()
	}()
	select {
	case parent.subSem <- struct{}{}:
	case <-ctx.Done():
		return tools.Result{Text: "Cancelled", IsError: true}
	}
	defer func() { <-parent.subSem }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	err := c.turn(ctx, agent.Input{Text: prompt})
	end := Event{Kind: EventSubagentEnd, Session: c.session.ID}
	if err != nil {
		end.Text = err.Error()
	}
	c.top.emit(end)
	return host.Report(ctx, c.session, err)
}

func (l link) Send(to, text string, followUp bool) error {
	return l.r.bus.Send(l.r.session.ID, to, text, followUp)
}

func (l link) Wait(ctx context.Context, from string, timeout time.Duration) (string, string, bool) {
	m, ok := l.r.bus.Wait(ctx, l.r.session.ID, from, timeout)
	if !ok {
		return "", "", false
	}
	return host.Sender(m), m.Text, true
}

func (l link) Peers() []tools.Peer { return host.Peers(l.r.bus, l.r.session.ID) }
