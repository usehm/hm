// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usehm/hm/internal/agent/provider"
)

// CatAgent is for tools that start or talk to other agents.
const CatAgent = "agent"

// Task is a piece of work handed to a sub-agent.
type Task struct {
	// Description names the sub-agent in the UI and in messages.
	Description string
	Prompt      string
	// Write lets the sub-agent edit files and run commands; without it the
	// sub-agent can only read.
	Write bool
	// Background returns at once; the sub-agent's report arrives later as
	// a message.
	Background bool
}

// Peer is another agent that messages can reach.
type Peer struct {
	ID, Name string
	// Role is how the peer relates to the caller: parent, child or sibling.
	Role    string
	Running bool
}

// Agents is how an agent's tools reach other agents. The app implements
// it for each agent it runs.
type Agents interface {
	// Spawn runs t in a new session and returns its report; callID is the
	// task call's ID, for showing the sub-agent's progress under it.
	Spawn(ctx context.Context, callID string, t Task) Result
	// Send delivers text to the agent named to: an ID, a name, or
	// "parent". A follow-up waits for the receiver to end its turn;
	// otherwise the text steers it, skipping its calls not yet started.
	Send(to, text string, followUp bool) error
	// Wait returns the next message for the caller, from anyone when from
	// is empty; ok is false after timeout.
	Wait(ctx context.Context, from string, timeout time.Duration) (sender, text string, ok bool)
	Peers() []Peer
}

// AgentTools are the tools for working with other agents; spawn adds the
// task tool, which nested agents past the depth limit go without.
func AgentTools(ag Agents, spawn bool) []Tool {
	out := []Tool{sendTool{ag}, waitTool{ag}, peersTool{ag}}
	if spawn {
		out = append(out, taskTool{ag})
	}
	return out
}

type callIDKey struct{}

// WithCallID marks ctx with the tool call it runs for.
func WithCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callIDKey{}, id)
}

// CallID is the tool call ctx runs for; empty outside one.
func CallID(ctx context.Context) string {
	id, _ := ctx.Value(callIDKey{}).(string)
	return id
}

// --- task -------------------------------------------------------------------

type taskTool struct{ ag Agents }

func (taskTool) Spec() provider.Tool {
	return provider.Tool{Name: "task", Description: "Hand a self-contained piece of work to a sub-agent, which works " +
		"in its own session with a fresh context and reports back. Use it for independent research or changes " +
		"that would crowd your context, and start several in one message to run them at once. The prompt is " +
		"all the sub-agent knows: say what to do, where to look and what to report. By default it can only " +
		"read; set write to let it edit files and run commands. With background the call returns at once and " +
		"the report arrives later as a message (see wait_message).",
		Schema: schema(map[string]any{
			"description": str("A few words naming the work, such as \"find the config loader\""),
			"prompt":      str("The full instructions for the sub-agent"),
			"write":       boolean("Let the sub-agent edit files and run commands (default false)"),
			"background":  boolean("Return at once; the report arrives as a message (default false)"),
		}, "description", "prompt")}
}

func (t taskTool) Prepare(_ Env, input json.RawMessage) (Plan, error) {
	var in struct {
		Description, Prompt string
		Write, Background   bool
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	in.Description = strings.TrimSpace(in.Description)
	if in.Description == "" || strings.TrimSpace(in.Prompt) == "" {
		return Plan{}, errors.New("description and prompt are required")
	}
	task := Task{Description: in.Description, Prompt: in.Prompt, Write: in.Write, Background: in.Background}
	summary := in.Description
	if in.Write {
		summary += " (may edit)"
	}
	if in.Background {
		summary += " (background)"
	}
	return Plan{Category: CatAgent, Summary: summary, GrantKey: "task", Run: func(ctx context.Context) Result {
		return t.ag.Spawn(ctx, CallID(ctx), task)
	}}, nil
}

// --- send_message -----------------------------------------------------------

type sendTool struct{ ag Agents }

func (sendTool) Spec() provider.Tool {
	return provider.Tool{Name: "send_message", Description: "Send a message to another running agent: your parent, " +
		"a sub-agent you started, or a sibling (list_agents shows who is there). The receiver reads it after " +
		"its current tool call and skips the calls it has not started, unless follow_up is set. For a request " +
		"and reply, send, then wait_message for the answer.",
		Schema: schema(map[string]any{
			"to":        str("The agent's ID or name, or \"parent\""),
			"text":      str("The message"),
			"follow_up": boolean("Deliver after the receiver ends its turn instead of interrupting its tool calls"),
		}, "to", "text")}
}

func (t sendTool) Prepare(_ Env, input json.RawMessage) (Plan, error) {
	var in struct {
		To, Text string
		FollowUp bool `json:"follow_up"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(in.To) == "" || strings.TrimSpace(in.Text) == "" {
		return Plan{}, errors.New("to and text are required")
	}
	return Plan{Category: CatAgent, Summary: "to " + in.To + ": " + firstLine(in.Text), Run: func(context.Context) Result {
		if err := t.ag.Send(in.To, in.Text, in.FollowUp); err != nil {
			return Result{Text: err.Error(), IsError: true}
		}
		return ok("Sent to %s", in.To)
	}}, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// --- wait_message -----------------------------------------------------------

type waitTool struct{ ag Agents }

func (waitTool) Spec() provider.Tool {
	return provider.Tool{Name: "wait_message", Description: "Wait for a message from another agent, such as the " +
		"report of a background task or the reply to send_message. Returns the sender and text, or says the " +
		"wait timed out.",
		Schema: schema(map[string]any{
			"from":            str("Only a message from this agent's ID or name; empty takes any"),
			"timeout_seconds": integer("How long to wait, 1 to 600 (default 120)"),
		})}
}

func (t waitTool) Prepare(_ Env, input json.RawMessage) (Plan, error) {
	var in struct {
		From           string
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if err := decode(input, &in); err != nil {
		return Plan{}, err
	}
	secs := in.TimeoutSeconds
	if secs <= 0 {
		secs = 120
	}
	secs = min(secs, 600)
	summary := fmt.Sprintf("up to %ds", secs)
	if in.From != "" {
		summary = "from " + in.From + ", " + summary
	}
	return Plan{Category: CatAgent, Summary: summary, Run: func(ctx context.Context) Result {
		from, text, got := t.ag.Wait(ctx, in.From, time.Duration(secs)*time.Second)
		if !got {
			if ctx.Err() != nil {
				return Result{Text: "Cancelled", IsError: true}
			}
			return ok("No message within %ds", secs)
		}
		return ok("Message from %s:\n%s", from, text)
	}}, nil
}

// --- list_agents ------------------------------------------------------------

type peersTool struct{ ag Agents }

func (peersTool) Spec() provider.Tool {
	return provider.Tool{Name: "list_agents", Description: "List the other agents you can message, with their " +
		"IDs, names, how they relate to you and whether they are running.",
		Schema: schema(map[string]any{})}
}

func (t peersTool) Prepare(_ Env, _ json.RawMessage) (Plan, error) {
	return Plan{Category: CatAgent, Summary: "agents", Run: func(context.Context) Result {
		peers := t.ag.Peers()
		if len(peers) == 0 {
			return ok("No other agents")
		}
		var b strings.Builder
		for _, p := range peers {
			state := "idle"
			if p.Running {
				state = "running"
			}
			fmt.Fprintf(&b, "%s  %s  (%s, %s)\n", p.ID, p.Name, p.Role, state)
		}
		return Result{Text: strings.TrimSpace(b.String())}
	}}, nil
}
