package agent

import (
	"sync"

	"github.com/mirzakhany/hm/internal/agent/tools"
)

// Decision is what the policy says about a tool call.
type Decision int

const (
	Allow Decision = iota
	Ask
	Deny
)

func (d Decision) String() string {
	switch d {
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	}
	return "auto"
}

// defaultApproval applies when a mode says nothing about a tool or its
// category. Edits apply at once (with an undo checkpoint per turn); a mode
// that sets edit: ask gets every edit as a diff to review.
var defaultApproval = map[string]string{
	tools.CatRead:    "auto",
	tools.CatMeta:    "auto",
	tools.CatNetwork: "auto",
	tools.CatEdit:    "auto",
	tools.CatDelete:  "ask",
	tools.CatShell:   "ask",
	tools.CatGitPush: "ask",
}

// Policy decides which tool calls run, ask, or are refused.
type Policy struct {
	Root  string
	Rules map[string]string // tool name or category -> auto | ask | deny

	mu      sync.Mutex
	granted map[string]bool // "allow for this session" answers
}

// NewPolicy builds the policy for a mode.
func NewPolicy(root string, rules map[string]string) *Policy {
	return &Policy{Root: root, Rules: rules, granted: map[string]bool{}}
}

// Decide rules on one call and says why when it asks or refuses.
func (p *Policy) Decide(tool string, plan tools.Plan) (Decision, string) {
	rule := p.rule(tool, plan.Category)
	if rule == "deny" {
		return Deny, "this mode does not allow " + tool
	}
	// Some calls always ask, whatever the mode says.
	if plan.Category == tools.CatGitPush {
		return Ask, "destructive git commands always ask"
	}
	for _, path := range plan.Paths {
		if path != "" && !tools.Inside(p.Root, path) {
			return Ask, "it reaches outside the workspace"
		}
	}
	if rule == "ask" {
		if p.isGranted(grantKey(tool, plan)) {
			return Allow, ""
		}
		return Ask, ""
	}
	return Allow, ""
}

func (p *Policy) rule(tool, category string) string {
	if r, ok := p.Rules[tool]; ok {
		return r
	}
	if r, ok := p.Rules[category]; ok {
		return r
	}
	if r, ok := defaultApproval[category]; ok {
		return r
	}
	return "ask"
}

// CanGrant reports whether "allow for this session" may be offered.
func (p *Policy) CanGrant(plan tools.Plan) bool {
	if plan.Category == tools.CatGitPush {
		return false
	}
	for _, path := range plan.Paths {
		if path != "" && !tools.Inside(p.Root, path) {
			return false
		}
	}
	return true
}

// Grant allows calls like this one for the rest of the session.
func (p *Policy) Grant(tool string, plan tools.Plan) {
	p.mu.Lock()
	p.granted[grantKey(tool, plan)] = true
	p.mu.Unlock()
}

func (p *Policy) isGranted(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.granted[key]
}

func grantKey(tool string, plan tools.Plan) string {
	if plan.GrantKey != "" {
		return plan.GrantKey
	}
	return tool
}
