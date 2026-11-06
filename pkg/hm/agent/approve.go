package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// noApprover is the model's answer when nobody can approve a call.
const noApprover = "Refused: hm runs without a user to approve this call. Do without it, or say what you need approved."

// label names a sub-agent in what it asks; empty for the top session.
func (r *Runner) label() string {
	if r.parent == nil {
		return ""
	}
	return "sub-agent \"" + r.session.Title + "\""
}

// approve hands a call waiting for approval to the Approver.
func (r *Runner) approve(ctx context.Context, a agent.Approval) agent.Answer {
	ap := r.opts.Approver
	if ap == nil {
		return agent.Answer{Note: noApprover}
	}
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
	d, err := ap.Approve(ctx, req)
	if err != nil {
		return agent.Answer{Note: "Refused: the approval failed: " + err.Error()}
	}
	ans := agent.Answer{Allow: d.Allow, ForSession: d.ForSession && a.CanGrant, Note: d.Note}
	if d.Allow && d.Edits != nil {
		ans.Edits = chosenEdits(a.Plan.Edits, d.Edits)
	}
	return ans
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
