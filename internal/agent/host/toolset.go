package host

import (
	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/skills"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// ReadOnlyTools are what a sub-agent without write may use: reading,
// searching, the web, and the other agents.
var ReadOnlyTools = []string{"read", "ls", "glob", "grep", "search_project", "lsp_*", "git_status", "git_diff",
	"git_log", "web_search", "web_fetch", "todo", "skill", "task", "send_message", "wait_message", "list_agents"}

// ToolsetOptions are the parts of a toolset that come from the host
// rather than the config.
type ToolsetOptions struct {
	Mode   agent.Mode // set by Build from Turn.Mode
	Shells *tools.Shells
	Todos  *tools.Todos
	// Code is the language-server tools' backend; nil leaves them out.
	Code tools.Code
	// Agents reaches other agents; nil leaves the agent tools out. Spawn
	// offers the task tool too.
	Agents tools.Agents
	Spawn  bool
	// Subagent is set for a sub-agent's turn; without Write it gets
	// ReadOnlyTools only.
	Subagent, Write bool
}

// Toolset is what one agent turn may use.
type Toolset struct {
	Reg       *tools.Registry
	Rules     map[string]string
	Extra     string // added to the system prompt: the skills
	ReadRoots []string
}

// Toolset builds the tools for a mode: built-ins, language tools, web
// tools, the config's command tools, MCP tools and the skill tool. The
// mode's tool list filters all of them.
func (h *Host) Toolset(o ToolsetOptions) Toolset {
	cfg := h.Config
	all := tools.Builtin(o.Shells, o.Todos)
	if o.Code != nil {
		all = append(all, tools.CodeTools(o.Code)...)
	}
	all = append(all, tools.WebTools(cfg.Tools.Web.Search)...)
	builtinNames := map[string]bool{}
	for _, t := range all {
		builtinNames[t.Spec().Name] = true
	}
	rules := map[string]string{}
	cmds, errs := tools.CommandTools(cfg.Tools.Commands)
	for _, err := range errs {
		h.warn(err.Error())
	}
	for _, t := range cmds {
		name := t.Spec().Name
		if builtinNames[name] {
			h.warn("tools.commands." + name + " has the name of a built-in tool and is skipped")
			continue
		}
		all = append(all, t)
		if ap := cfg.Tools.Commands[name].Approval; ap != "" {
			rules[name] = ap
		}
	}
	if h.MCP != nil {
		all = append(all, h.MCP.Tools()...)
		for k, v := range h.MCP.Approvals() {
			rules[k] = v
		}
	}
	if o.Agents != nil {
		all = append(all, tools.AgentTools(o.Agents, o.Spawn)...)
	}
	reg := tools.NewRegistry(all...).Filter(o.Mode.Tools)
	if o.Subagent && !o.Write {
		reg = reg.Filter(ReadOnlyTools)
	}

	var ts Toolset
	dirs := skills.Dirs(BuiltinSkillsDir(), cfg.Tools.Skills.Paths, h.Root)
	list, serrs := skills.Load(dirs)
	for _, err := range serrs {
		h.warn("Skill: " + err.Error())
	}
	if len(list) > 0 {
		// Skills are reading material; every mode may load them.
		reg.Add(skills.Tool(list))
		ts.Extra = skills.Prompt(list, o.Mode.Skills)
		for _, s := range list {
			ts.ReadRoots = append(ts.ReadRoots, s.Dir)
		}
	}
	// The mode's own rules win over per-command and per-server defaults.
	for k, v := range o.Mode.Approval {
		rules[k] = v
	}
	ts.Reg, ts.Rules = reg, rules
	return ts
}
