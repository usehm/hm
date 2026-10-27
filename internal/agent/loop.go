package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirzakhany/hm/internal/agent/guard"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// maxSteps bounds model calls in one turn, so a confused loop stops.
const maxSteps = 80

// EventKind tags what the agent reports while it works.
type EventKind int

const (
	EvText       EventKind = iota // Text: more answer text
	EvThinking                    // Text: more reasoning summary
	EvToolStart                   // Tool: the model began a call (input still streaming)
	EvToolCall                    // a call was planned: Tool, CallID, Summary
	EvToolResult                  // Tool, CallID, Result
	EvMessage                     // a message joined the session (Session is up to date)
	EvUsage                       // Usage: this call's tokens
	EvError                       // Text: what went wrong
)

// Event is one report.
type Event struct {
	Kind    EventKind
	Text    string
	Tool    string
	CallID  string
	Summary string
	Result  tools.Result
	Usage   provider.Usage
}

// Approval is a call waiting for the user.
type Approval struct {
	Tool     string
	CallID   string
	Plan     tools.Plan
	Reason   string // why it asks, beyond the mode's rule
	CanGrant bool   // "allow for this session" may be offered
}

// Answer is the user's ruling.
type Answer struct {
	Allow      bool
	ForSession bool
	// Edits, for a file-edit call, are what to apply: the plan's edits, or
	// some hunks of them. Nil applies the plan as is.
	Edits []tools.FileEdit
	Note  string // passed to the model, such as why it was refused
}

// Hooks connect the loop to the app. Every hook may block; the loop runs on
// its own goroutine.
type Hooks struct {
	Event   func(Event)
	Approve func(ctx context.Context, a Approval) Answer
	Ask     func(ctx context.Context, question string) (string, error)
	// Apply writes file edits (New == nil deletes) and records them for
	// "undo agent turn".
	Apply func(ctx context.Context, edits []tools.FileEdit) error
	// Blocked is told when the guard refuses a call.
	Blocked func(tool, reason string)
}

// Agent runs turns of one session.
type Agent struct {
	Provider provider.Provider
	Model    string
	Effort   string
	System   string
	Tools    *tools.Registry
	Policy   *Policy
	// Guard checks each call against guard.yaml before the policy; nil
	// checks nothing.
	Guard *guard.Guard
	Env   tools.Env
	Hooks Hooks
}

func (a *Agent) emit(e Event) {
	if a.Hooks.Event != nil {
		a.Hooks.Event(e)
	}
}

// Run adds the user's message and works until the model ends its turn,
// asking for approvals on the way. The session grows append-only: earlier
// turns are never edited, so cached prefixes and thinking stay valid.
func (a *Agent) Run(ctx context.Context, s *Session, input string) error {
	s.append(provider.Message{Role: provider.User, Blocks: []provider.Block{{Type: provider.Text, Text: input}}})
	a.emit(Event{Kind: EvMessage})
	for step := 0; step < maxSteps; step++ {
		req := provider.Request{
			Model: a.Model, System: a.System, Messages: s.Snapshot(),
			Tools: a.Tools.Specs(), Effort: a.Effort,
		}
		resp, err := a.Provider.Stream(ctx, req, func(e provider.Event) {
			switch e.Kind {
			case provider.EventText:
				a.emit(Event{Kind: EvText, Text: e.Text})
			case provider.EventThinking:
				a.emit(Event{Kind: EvThinking, Text: e.Text})
			case provider.EventToolStart:
				a.emit(Event{Kind: EvToolStart, Tool: e.ToolName})
			}
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.emit(Event{Kind: EvError, Text: err.Error()})
			return err
		}
		s.AddUsage(resp.Usage)
		s.append(resp.Message)
		a.emit(Event{Kind: EvUsage, Usage: resp.Usage})
		a.emit(Event{Kind: EvMessage})

		switch resp.StopReason {
		case provider.StopRefusal:
			msg := "The model declined this request"
			if resp.StopDetail != "" {
				msg += " (" + resp.StopDetail + ")"
			}
			a.emit(Event{Kind: EvError, Text: msg})
			return nil
		case provider.StopToolUse, provider.StopMaxTokens:
		default:
			return nil
		}
		calls := resp.Message.ToolUses()
		if len(calls) == 0 {
			if resp.StopReason == provider.StopMaxTokens {
				a.emit(Event{Kind: EvError, Text: "The response hit the token limit"})
			}
			return nil
		}
		// Every tool result goes back in one user message.
		results := make([]provider.Block, 0, len(calls))
		for _, call := range calls {
			res := a.call(ctx, call)
			if ctx.Err() != nil {
				res = tools.Result{Text: "Cancelled by the user", IsError: true}
			}
			results = append(results, provider.Block{Type: provider.ToolResult, ToolID: call.ToolID,
				ToolName: call.ToolName, Text: res.Text, IsError: res.IsError})
			a.emit(Event{Kind: EvToolResult, Tool: call.ToolName, CallID: call.ToolID, Result: res})
		}
		s.append(provider.Message{Role: provider.User, Blocks: results})
		a.emit(Event{Kind: EvMessage})
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	a.emit(Event{Kind: EvError, Text: fmt.Sprintf("Stopped after %d steps", maxSteps)})
	return nil
}

// call runs one tool call through the policy.
func (a *Agent) call(ctx context.Context, call provider.Block) tools.Result {
	t, ok := a.Tools.Get(call.ToolName)
	if !ok {
		return tools.Result{Text: "Unknown tool " + call.ToolName + " (not available in this mode)", IsError: true}
	}
	plan, err := t.Prepare(a.Env, call.Input)
	if err != nil {
		return tools.Result{Text: err.Error(), IsError: true}
	}
	a.emit(Event{Kind: EvToolCall, Tool: call.ToolName, CallID: call.ToolID, Summary: plan.Summary})

	verdict, why := a.Guard.Check(call.ToolName, plan)
	if verdict == guard.Deny {
		if a.Hooks.Blocked != nil {
			a.Hooks.Blocked(call.ToolName, why)
		}
		return tools.Result{Text: "Blocked by the user's guard rules (guard.yaml): " + why +
			". Do not try to get around this; tell the user what you needed instead.", IsError: true}
	}
	decision, reason := a.Policy.Decide(call.ToolName, plan)
	switch {
	case verdict == guard.Ask && decision != Deny:
		decision, reason = Ask, why // guard rules ask every time, grants or not
	case verdict == guard.Allow && decision == Ask:
		decision = Allow
	}
	ans := Answer{Allow: decision == Allow}
	switch decision {
	case Deny:
		return tools.Result{Text: "Refused: " + reason, IsError: true}
	case Ask:
		if a.Hooks.Approve == nil {
			return tools.Result{Text: "Refused: no one to approve it", IsError: true}
		}
		ans = a.Hooks.Approve(ctx, Approval{Tool: call.ToolName, CallID: call.ToolID, Plan: plan,
			Reason: reason, CanGrant: a.Policy.CanGrant(plan)})
		if ctx.Err() != nil {
			return tools.Result{Text: "Cancelled by the user", IsError: true}
		}
		if !ans.Allow {
			msg := "The user rejected this"
			if ans.Note != "" {
				msg += ": " + ans.Note
			}
			return tools.Result{Text: msg, IsError: true}
		}
		if ans.ForSession {
			a.Policy.Grant(call.ToolName, plan)
		}
	}

	switch {
	case plan.Question != "":
		if a.Hooks.Ask == nil {
			return tools.Result{Text: "No one can answer right now", IsError: true}
		}
		answer, err := a.Hooks.Ask(ctx, plan.Question)
		if err != nil {
			return tools.Result{Text: err.Error(), IsError: true}
		}
		return tools.Result{Text: answer}
	case len(plan.Edits) > 0:
		edits := plan.Edits
		if ans.Edits != nil {
			edits = ans.Edits
		}
		if len(edits) == 0 {
			return tools.Result{Text: "The user rejected every change in this edit", IsError: true}
		}
		if err := a.Hooks.Apply(ctx, edits); err != nil {
			return tools.Result{Text: err.Error(), IsError: true}
		}
		return tools.Result{Text: describeEdits(a.Env, plan.Edits, edits, ans.Note)}
	case plan.Run != nil:
		return plan.Run(ctx)
	}
	return tools.Result{Text: "Nothing to do"}
}

// describeEdits tells the model what was applied, including when the user
// kept only some of the changes.
func describeEdits(env tools.Env, planned, applied []tools.FileEdit, note string) string {
	var b strings.Builder
	for _, e := range applied {
		switch {
		case e.New == nil:
			fmt.Fprintf(&b, "Deleted %s\n", short(env, e.Path))
		case e.Old == nil:
			fmt.Fprintf(&b, "Created %s\n", short(env, e.Path))
		default:
			fmt.Fprintf(&b, "Edited %s\n", short(env, e.Path))
		}
	}
	for i, e := range applied {
		if i < len(planned) && string(planned[i].New) != string(e.New) {
			fmt.Fprintf(&b, "The user accepted only part of the change to %s; read it before editing it again.\n", short(env, e.Path))
		}
	}
	if note != "" {
		fmt.Fprintf(&b, "The user said: %s\n", note)
	}
	return strings.TrimSpace(b.String())
}

func short(env tools.Env, path string) string {
	root := env.Root()
	if strings.HasPrefix(path, root+"/") {
		return path[len(root)+1:]
	}
	return path
}

// ErrBusy is returned when a turn is already running.
var ErrBusy = errors.New("the agent is already working")

// now is the clock, for tests.
var now = time.Now
