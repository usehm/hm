package agent

import (
	"context"
	"errors"

	"github.com/mirzakhany/hm/internal/agent"
)

// ErrBusy is returned when a turn is already running; send more with Steer
// or FollowUp instead.
var ErrBusy = errors.New("a turn is running; steer it or queue a follow-up")

// ErrClosed is returned once the Runner is closed.
var ErrClosed = errors.New("the runner is closed")

// ErrMaxSteps ends a turn that used up Options.MaxSteps model calls.
var ErrMaxSteps = agent.ErrMaxSteps

// ErrNothingToCompact is returned by Compact when the session is too short
// to summarise.
var ErrNothingToCompact = agent.ErrNothingToCompact

// Approver decides the tool calls the configured rules say to ask about.
// It is called on the agent's goroutine and may block, say to ask a
// person; ctx ends when the turn is cancelled. guard.yaml deny rules are
// checked before it and always win.
type Approver interface {
	Approve(ctx context.Context, req Request) (Decision, error)
}

// Asker answers the agent's ask_user questions. An Approver that also
// implements Asker gets them; without one, ask_user fails and the model
// is told nobody can answer.
type Asker interface {
	Ask(ctx context.Context, question string) (string, error)
}

// Request is a tool call waiting for approval.
type Request struct {
	Session string // the asking session; a sub-agent's is not the Runner's
	Tool    string
	CallID  string
	// Category is the kind of call: "read", "edit", "delete", "shell",
	// "network" and so on.
	Category string
	// Summary is the exact command, or what changes.
	Summary string
	// Reason is why it asks, beyond the mode's rule.
	Reason string
	// Command is the command line a shell or command tool runs.
	Command string
	Paths   []string
	// Edits are the file changes of an edit call.
	Edits []Edit
	// CanGrant says Decision.ForSession may be honoured.
	CanGrant bool
}

// Decision is the Approver's ruling on a Request.
type Decision struct {
	Allow bool
	// ForSession allows calls like this one for the rest of the session,
	// when the Request's CanGrant allows it.
	ForSession bool
	// Note goes to the model, such as why a call was refused.
	Note string
	// Edits, when set, are what to apply of an edit call: some of the
	// Request's files, with New changed to keep only some hunks. Files the
	// Request does not name are ignored. Nil applies the Request as is.
	Edits []Edit
}

// Edit is a change to one file. Old is nil for a new file; New is nil when
// the file is deleted.
type Edit struct {
	Path     string
	Old, New []byte
}

// EventKind names what an Event reports.
type EventKind string

const (
	EventText        EventKind = "text_delta"     // Text: more answer text
	EventThinking    EventKind = "thinking_delta" // Text: more reasoning summary
	EventToolStart   EventKind = "tool_start"     // Tool: the model began a call
	EventToolCall    EventKind = "tool_call"      // Tool, CallID, Summary: a call was planned
	EventToolResult  EventKind = "tool_result"    // Tool, CallID, Result
	EventMessage     EventKind = "message"        // a message joined the session
	EventUsage       EventKind = "usage"          // Usage: one model call's tokens
	EventError       EventKind = "error"          // Text: what went wrong
	EventRetry       EventKind = "retry"          // Text: a model call failed and is retried soon
	EventCompacting  EventKind = "compacting"     // older messages are being summarised
	EventCompacted   EventKind = "compacted"      // Text: what it saved; empty when it failed
	EventBlocked     EventKind = "blocked"        // Tool, Text: guard.yaml refused a call
	EventTurnEnd     EventKind = "turn_end"       // Text: the error that ended it, if any
	EventSubagentEnd EventKind = "subagent_end"   // a sub-agent's turn ended; Session is its own
)

// Event is something that happened in a turn. Session names the session
// it happened in: the Runner's, or one of its sub-agents'.
type Event struct {
	Kind    EventKind
	Session string
	Text    string
	Tool    string
	CallID  string
	Summary string
	Result  *ToolResult
	Usage   *Usage
}

// ToolResult is what a tool call gave back to the model.
type ToolResult struct {
	Text    string
	IsError bool
}

// Usage counts tokens; Cost is in USD, 0 when unknown.
type Usage struct {
	Input, Output, CacheRead, CacheWrite int64
	Cost                                 float64
}

// SessionInfo describes the Runner's session.
type SessionInfo struct {
	ID, Title      string
	Root           string
	Mode, Provider string
	Model          string // empty for the provider's default
	Status         string // "", "running" or "interrupted"
	Usage          Usage  // every model call of the session
	Context        int64  // tokens the next model call about sends
	Saved          bool   // false with Options.NoSession
}

var eventKinds = map[agent.EventKind]EventKind{
	agent.EvText: EventText, agent.EvThinking: EventThinking, agent.EvToolStart: EventToolStart,
	agent.EvToolCall: EventToolCall, agent.EvToolResult: EventToolResult, agent.EvMessage: EventMessage,
	agent.EvUsage: EventUsage, agent.EvError: EventError, agent.EvRetry: EventRetry,
	agent.EvCompacting: EventCompacting, agent.EvCompacted: EventCompacted,
}

// event converts one of the loop's events in session id.
func event(e agent.Event, id string) Event {
	out := Event{Kind: eventKinds[e.Kind], Session: id, Text: e.Text, Tool: e.Tool, CallID: e.CallID, Summary: e.Summary}
	switch e.Kind {
	case agent.EvToolResult:
		out.Result = &ToolResult{Text: e.Result.Text, IsError: e.Result.IsError}
	case agent.EvUsage:
		u := e.Usage
		out.Usage = &Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Cost: u.Cost}
	}
	return out
}
