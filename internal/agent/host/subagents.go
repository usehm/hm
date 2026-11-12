// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"context"
	"fmt"
	"strings"

	"github.com/usehm/hm/internal/agent"
	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/agent/tools"
)

// SubagentPrompt tells a sub-agent what it is.
func SubagentPrompt(write bool) string {
	access := "You can read but not edit files or run commands."
	if write {
		access = "You may edit files and run commands; the user approves them as for any agent."
	}
	return "# Sub-agent\nYou are a sub-agent: another agent handed you one task. " + access +
		" Work on the task alone; the user does not see your messages, only your last one goes back to the " +
		"agent that started you. End with a complete, self-contained report of what you found or did. " +
		"Your parent is reachable as \"parent\" with send_message."
}

// NewSubagentSession makes the session of a sub-agent of parent working
// on task t.
func NewSubagentSession(parent *agent.Session, mode, prov string, t tools.Task) *agent.Session {
	s := agent.NewSession(mode, prov)
	s.Root, s.ProjectID, s.ParentID = parent.Root, parent.ProjectID, parent.ID
	s.Title = t.Description
	return s
}

// Report is what goes back to the parent once sub-agent session s ran
// its turn under ctx and returned err: its last message.
func Report(ctx context.Context, s *agent.Session, err error) tools.Result {
	report := LastAssistantText(s.Snapshot())
	switch {
	case err != nil && ctx.Err() != nil:
		return tools.Result{Text: "The sub-agent was cancelled. Its last words: " + report, IsError: true}
	case err != nil:
		return tools.Result{Text: "The sub-agent failed: " + err.Error() + "\n" + report, IsError: true}
	case report == "":
		return tools.Result{Text: "The sub-agent finished without a report", IsError: true}
	}
	return tools.Result{Text: report + fmt.Sprintf("\n\n(sub-agent session %s)", s.ID)}
}

// BackgroundStarted is the task tool's result for a sub-agent started in
// the background.
func BackgroundStarted(id string, t tools.Task) tools.Result {
	return tools.Result{Text: fmt.Sprintf("Started sub-agent %s (%q) in the background. Its report will arrive as a message; "+
		"wait_message from %s waits for it.", id, t.Description, id)}
}

// BackgroundReport is the message carrying a background task's report.
func BackgroundReport(t tools.Task, report tools.Result) string {
	return "Report of the background task \"" + t.Description + "\":\n" + report.Text
}

// LastAssistantText is the last thing the assistant said.
func LastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.Assistant {
			if t := strings.TrimSpace(msgs[i].TextOf()); t != "" {
				return t
			}
		}
	}
	return ""
}

// MailText is how another agent's message reaches the model.
func MailText(m agent.Mail) string {
	return fmt.Sprintf("<agent_message from=%q name=%q>\n%s\n</agent_message>", m.From, m.FromName, m.Text)
}

// Sender names who sent m, for wait_message.
func Sender(m agent.Mail) string {
	if m.FromName != "" {
		return m.From + " (" + m.FromName + ")"
	}
	return m.From
}

// Peers converts the bus's view of the other agents for list_agents.
func Peers(b *agent.Bus, id string) []tools.Peer {
	var out []tools.Peer
	for _, p := range b.Peers(id) {
		out = append(out, tools.Peer{ID: p.ID, Name: p.Name, Role: p.Role, Running: p.Running})
	}
	return out
}
