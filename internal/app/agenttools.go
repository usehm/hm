package app

import (
	"context"
	"time"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent/host"
	"github.com/mirzakhany/hm/internal/agent/mcp"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/config"
)

// agentHost is what the app's agents are built from, with the config as
// it is now.
func (a *App) agentHost() *host.Host {
	return &host.Host{Root: a.ws.Root, Config: a.cfg.Current(), Projects: a.agent.projects, MCP: a.agent.mcp,
		Warn: a.agentWarn, Registry: a.modelRegistry()}
}

func (a *App) agentWarn(msg string) { a.notifs.Add(msg, ui.ToastWarning) }

// modelRegistry is hm's model table with the models.json files on top,
// read once per workspace and again when one of them changes.
func (a *App) modelRegistry() *provider.Registry {
	st := &a.agent
	if st.registry == nil || st.registryRoot != a.ws.Root {
		var err error
		st.registry, err = host.LoadModels(a.ws.Root)
		st.registryRoot = a.ws.Root
		if err != nil {
			a.agentWarn(err.Error())
		}
	}
	return st.registry
}

// agentTools are the tools of r beyond the config's: the editor's
// language tools and the other agents.
func (a *App) agentTools(r *agentRun) host.ToolsetOptions {
	return host.ToolsetOptions{Shells: r.shells, Todos: r.todos, Code: agentCode{a},
		Agents: agentLink{a, r}, Spawn: r.depth < a.cfg.Current().Agent.Subagents.MaxDepth,
		Subagent: r.parent != nil, Write: r.write}
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
