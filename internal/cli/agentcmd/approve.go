// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package agentcmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/usehm/hm/pkg/hm/agent"
)

// Approval policies for --approve.
const (
	approveNever = "never" // refuse every call that needs approval
	approveEdits = "edits" // allow file edits, refuse the rest
	approveAll   = "all"   // allow every call the mode asks about
)

// policy approves calls by --approve, with nobody to ask, and remembers
// what it refused for the exit code.
type policy struct {
	mode string

	mu      sync.Mutex
	refused []string // tools refused, in order
}

func (p *policy) Approve(_ context.Context, r agent.Request) (agent.Decision, error) {
	if p.mode == approveAll || (p.mode == approveEdits && r.Category == "edit") {
		return agent.Decision{Allow: true}, nil
	}
	p.mu.Lock()
	p.refused = append(p.refused, r.Tool)
	p.mu.Unlock()
	rerun := "--approve=all"
	if p.mode == approveNever && r.Category == "edit" {
		rerun = "--approve=edits or --approve=all"
	}
	return agent.Decision{Note: fmt.Sprintf("hm agent runs with --approve=%s, so nobody can allow %s. "+
		"Do without it, or tell the user to rerun with %s.", p.mode, r.Tool, rerun)}, nil
}

// refusals lists the tools refused so far.
func (p *policy) refusals() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.refused...)
}
