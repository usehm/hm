package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// noApprover is why a call is refused when nobody can approve it; the
// model reads it after "The user rejected this: ".
const noApprover = "hm runs without a user to approve this call. Do without it, or say what you need approved."

// label names a sub-agent in what it asks; empty for the top session.
func (r *Runner) label() string {
	if r.parent == nil {
		return ""
	}
	return "sub-agent \"" + r.session.Title + "\""
}

// approve hands a call waiting for approval to the Approver, telling
// Events before and after.
func (r *Runner) approve(ctx context.Context, a agent.Approval) agent.Answer {
	req := Request{
		Session: r.session.ID, Tool: a.Tool, CallID: a.CallID, Category: a.Plan.Category,
		Summary: a.Plan.Summary, Reason: a.Reason, Command: a.Plan.Command,
		Paths: append([]string(nil), a.Plan.Paths...), CanGrant: a.CanGrant,
	}
	if l := r.label(); l != "" {
		req.Reason = strings.TrimSpace(l + " asks; " + req.Reason)
	}
	for _, e := range a.Plan.Edits {
		req.Edits = append(req.Edits, Edit{Path: e.Path, Old: e.Old, New: e.New})
	}
	ev := Event{Kind: EventApprovalNeeded, Session: req.Session, Tool: req.Tool, CallID: req.CallID, Request: &req}
	r.top.emit(ev)
	d := r.decide(ctx, req)
	ev.Kind, ev.Decision = EventApproval, &d
	r.top.emit(ev)
	ans := agent.Answer{Allow: d.Allow, ForSession: d.ForSession && a.CanGrant, Note: d.Note}
	if d.Allow && d.Edits != nil {
		ans.Edits = chosenEdits(a.Plan.Edits, d.Edits)
	}
	return ans
}

// decide asks the Approver; without one, or when it fails, the call is
// refused.
func (r *Runner) decide(ctx context.Context, req Request) Decision {
	ap := r.opts.Approver
	if ap == nil {
		return Decision{Note: noApprover}
	}
	d, err := ap.Approve(ctx, req)
	if err != nil {
		return Decision{Note: "the approval failed: " + err.Error()}
	}
	return d
}

// chosenEdits are the files of plan the Approver kept, with its New. A
// file the plan does not name is dropped, so an Approver cannot write
// past the guard.
func chosenEdits(plan []tools.FileEdit, chosen []Edit) []tools.FileEdit {
	out := []tools.FileEdit{}
	for _, c := range chosen {
		for _, p := range plan {
			if p.Path == c.Path {
				if p.New == nil {
					c.New = nil // a delete is taken whole or not at all
				}
				out = append(out, tools.FileEdit{Path: p.Path, Old: p.Old, New: c.New})
				break
			}
		}
	}
	return out
}

// ask puts an ask_user question to the Approver, when it is an Asker.
func (r *Runner) ask(ctx context.Context, q string) (string, error) {
	ak, ok := r.opts.Approver.(Asker)
	if !ok {
		return "", errors.New("nobody can answer: hm runs without a user; decide yourself and say what you assumed")
	}
	if l := r.label(); l != "" {
		q = l + " asks: " + q
	}
	ans, err := ak.Ask(ctx, q)
	if err == nil && strings.TrimSpace(ans) == "" {
		err = errors.New("the user did not answer")
	}
	return ans, err
}
