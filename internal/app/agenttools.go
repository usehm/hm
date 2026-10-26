package app

import (
	"context"
	"path/filepath"
	"time"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/mcp"
	"github.com/mirzakhany/hm/internal/agent/skills"
	"github.com/mirzakhany/hm/internal/agent/tools"
	"github.com/mirzakhany/hm/internal/config"
)

// builtinSkillsDir is where the bundled skills are written for the agent.
func builtinSkillsDir() string { return filepath.Join(config.UserDir(), "skills-builtin") }

// toolset is what one agent turn may use.
type toolset struct {
	reg       *tools.Registry
	rules     map[string]string
	extra     string // added to the system prompt: the skills
	readRoots []string
}

// agentToolset builds the tools for a mode: built-ins, the editor's
// language tools, web tools, the config's command tools, MCP tools and the
// skill tool. The mode's tool list filters all of them.
func (a *App) agentToolset(mode agent.Mode) toolset {
	cfg := a.cfg.Current()
	st := &a.agent
	all := tools.Builtin(st.shells, st.todos)
	all = append(all, tools.CodeTools(agentCode{a})...)
	all = append(all, tools.WebTools(cfg.Tools.Web.Search)...)
	builtinNames := map[string]bool{}
	for _, t := range all {
		builtinNames[t.Spec().Name] = true
	}
	rules := map[string]string{}
	cmds, errs := tools.CommandTools(cfg.Tools.Commands)
	for _, err := range errs {
		a.notifs.Add(err.Error(), ui.ToastWarning)
	}
	for _, t := range cmds {
		name := t.Spec().Name
		if builtinNames[name] {
			a.notifs.Add("tools.commands."+name+" has the name of a built-in tool and is skipped", ui.ToastWarning)
			continue
		}
		all = append(all, t)
		if ap := cfg.Tools.Commands[name].Approval; ap != "" {
			rules[name] = ap
		}
	}
	if st.mcp != nil {
		all = append(all, st.mcp.Tools()...)
		for k, v := range st.mcp.Approvals() {
			rules[k] = v
		}
	}
	reg := tools.NewRegistry(all...).Filter(mode.Tools)

	var ts toolset
	dirs := skills.Dirs(builtinSkillsDir(), cfg.Tools.Skills.Paths, a.ws.Root)
	list, serrs := skills.Load(dirs)
	for _, err := range serrs {
		a.notifs.Add("Skill: "+err.Error(), ui.ToastWarning)
	}
	if len(list) > 0 {
		// Skills are reading material; every mode may load them.
		reg.Add(skills.Tool(list))
		ts.extra = skills.Prompt(list, mode.Skills)
		for _, s := range list {
			ts.readRoots = append(ts.readRoots, s.Dir)
		}
	}
	// The mode's own rules win over per-command and per-server defaults.
	for k, v := range mode.Approval {
		rules[k] = v
	}
	ts.reg, ts.rules = reg, rules
	return ts
}

// startMCP connects to the configured MCP servers in the background and
// reports the ones that fail.
func (a *App) startMCP(servers map[string]config.MCPServer) {
	m := a.agent.mcp
	if m == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		m.Apply(ctx, servers)
		a.onUI(func() {
			for _, s := range m.Servers() {
				if s.Err != nil {
					a.notifs.Add("MCP: "+s.Err.Error(), ui.ToastWarning)
				}
			}
		})
	}()
}

// newMCP replaces the MCP manager for the open folder.
func (a *App) newMCP() {
	if a.agent.mcp != nil {
		a.agent.mcp.Close()
	}
	a.agent.mcp = mcp.NewManager(a.ws.Root)
}
