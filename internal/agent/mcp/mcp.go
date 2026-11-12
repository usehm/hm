// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Package mcp connects the agent to Model Context Protocol servers from the
// config, over stdio or streamable HTTP, and offers their tools as agent
// tools named mcp__<server>__<tool>.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/agent/tools"
	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/version"
)

// CatMCP is the approval category of MCP tools; they ask by default, since
// a server can do anything.
const CatMCP = "mcp"

const (
	connectTimeout = 30 * time.Second
	callTimeout    = 5 * time.Minute
	resultMax      = 40000
)

// Server is one configured server and its state.
type Server struct {
	Name    string
	Config  config.MCPServer
	Tools   []*sdk.Tool
	Err     error
	session *sdk.ClientSession
}

// Manager owns the connections.
type Manager struct {
	mu      sync.Mutex
	servers map[string]*Server
	root    string
}

// NewManager returns a manager with no servers.
func NewManager(root string) *Manager { return &Manager{servers: map[string]*Server{}, root: root} }

// Apply connects to cfg's servers, reconnecting those whose settings
// changed and closing those removed. It blocks until every server answered
// or failed; call it off the UI goroutine.
func (m *Manager) Apply(ctx context.Context, cfg map[string]config.MCPServer) {
	m.mu.Lock()
	var stale []*Server
	for name, s := range m.servers {
		if c, ok := cfg[name]; !ok || !sameConfig(c, s.Config) {
			stale = append(stale, s)
			delete(m.servers, name)
		}
	}
	var todo []*Server
	for name, c := range cfg {
		if _, ok := m.servers[name]; !ok {
			s := &Server{Name: name, Config: c}
			m.servers[name] = s
			todo = append(todo, s)
		}
	}
	m.mu.Unlock()
	for _, s := range stale {
		if s.session != nil {
			_ = s.session.Close()
		}
	}
	var wg sync.WaitGroup
	for _, s := range todo {
		wg.Add(1)
		go func(s *Server) {
			defer wg.Done()
			sess, list, err := connect(ctx, s.Name, s.Config, m.root)
			m.mu.Lock()
			s.session, s.Tools, s.Err = sess, list, err
			m.mu.Unlock()
		}(s)
	}
	wg.Wait()
}

func sameConfig(a, b config.MCPServer) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// connect starts or reaches a server and lists its tools.
func connect(ctx context.Context, name string, c config.MCPServer, root string) (*sdk.ClientSession, []*sdk.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	var t sdk.Transport
	switch c.Transport {
	case "http":
		if c.URL == "" {
			return nil, nil, fmt.Errorf("mcp server %s: url is required for http", name)
		}
		headers := map[string]string{}
		for k, v := range c.Headers {
			r, err := config.Resolve(v)
			if err != nil {
				return nil, nil, fmt.Errorf("mcp server %s: header %s: %w", name, k, err)
			}
			headers[k] = r
		}
		t = &sdk.StreamableClientTransport{Endpoint: c.URL,
			HTTPClient: &http.Client{Transport: headerTransport{headers: headers, base: http.DefaultTransport}}}
	default:
		if c.Command == "" {
			return nil, nil, fmt.Errorf("mcp server %s: command is required for stdio", name)
		}
		cmd := exec.Command(config.ExpandHome(c.Command), c.Args...)
		cmd.Dir = root
		cmd.Env = os.Environ()
		for k, v := range c.Env {
			r, err := config.Resolve(v)
			if err != nil {
				return nil, nil, fmt.Errorf("mcp server %s: env %s: %w", name, k, err)
			}
			cmd.Env = append(cmd.Env, k+"="+r)
		}
		t = &sdk.CommandTransport{Command: cmd}
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "hm", Version: version.Version}, nil)
	sess, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp server %s: %w", name, err)
	}
	var list []*sdk.Tool
	for tool, err := range sess.Tools(ctx, nil) {
		if err != nil {
			_ = sess.Close()
			return nil, nil, fmt.Errorf("mcp server %s: listing tools: %w", name, err)
		}
		list = append(list, tool)
	}
	return sess, list, nil
}

// headerTransport adds configured headers, such as Authorization.
type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return h.base.RoundTrip(r)
}

// Servers lists the servers, sorted, for the settings and status views.
func (m *Manager) Servers() []Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Server, 0, len(m.servers))
	for _, s := range m.servers {
		out = append(out, Server{Name: s.Name, Config: s.Config, Tools: s.Tools, Err: s.Err})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Tools returns agent tools for every connected server's tools.
func (m *Manager) Tools() []tools.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []tools.Tool
	names := make([]string, 0, len(m.servers))
	for n := range m.servers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := m.servers[n]
		if s.session == nil {
			continue
		}
		for _, t := range s.Tools {
			out = append(out, &tool{server: s.Name, session: s.session, def: t})
		}
	}
	return out
}

// Approvals returns per-server approval rules, as tool-name globs.
func (m *Manager) Approvals() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for n, s := range m.servers {
		if s.Config.Approval != "" {
			out[ToolName(n, "*")] = s.Config.Approval
		}
	}
	return out
}

// Close ends every session.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.servers {
		if s.session != nil {
			_ = s.session.Close()
		}
	}
	m.servers = map[string]*Server{}
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// ToolName is the agent's name for a server's tool.
func ToolName(server, tool string) string {
	if tool == "*" {
		return "mcp__" + unsafeName.ReplaceAllString(server, "_") + "__*"
	}
	name := "mcp__" + unsafeName.ReplaceAllString(server, "_") + "__" + unsafeName.ReplaceAllString(tool, "_")
	if len(name) > 128 {
		name = name[:128]
	}
	return name
}

// tool adapts one MCP tool.
type tool struct {
	server  string
	session *sdk.ClientSession
	def     *sdk.Tool
}

func (t *tool) Spec() provider.Tool {
	schema, _ := json.Marshal(t.def.InputSchema)
	if len(schema) == 0 || string(schema) == "null" {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	desc := t.def.Description
	if desc == "" {
		desc = t.def.Name
	}
	return provider.Tool{Name: ToolName(t.server, t.def.Name),
		Description: "[MCP server " + t.server + "] " + desc, Schema: schema}
}

func (t *tool) Prepare(_ tools.Env, input json.RawMessage) (tools.Plan, error) {
	if !json.Valid(input) {
		return tools.Plan{}, fmt.Errorf("INVALID_JSON: the tool input is not valid JSON; send the call again")
	}
	args := json.RawMessage(input)
	summary := t.server + " · " + t.def.Name + " " + compact(input)
	return tools.Plan{Category: CatMCP, Summary: summary, Run: func(ctx context.Context) tools.Result {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		res, err := t.session.CallTool(ctx, &sdk.CallToolParams{Name: t.def.Name, Arguments: args})
		if err != nil {
			return tools.Result{Text: err.Error(), IsError: true}
		}
		return tools.Result{Text: resultText(res), IsError: res.IsError}
	}}, nil
}

func compact(input json.RawMessage) string {
	s := string(input)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// resultText flattens a result's content; output is data for the model,
// never instructions.
func resultText(r *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		switch v := c.(type) {
		case *sdk.TextContent:
			b.WriteString(v.Text)
			b.WriteString("\n")
		case *sdk.ImageContent:
			fmt.Fprintf(&b, "[image %s, %d bytes]\n", v.MIMEType, len(v.Data))
		case *sdk.EmbeddedResource:
			if v.Resource != nil {
				if v.Resource.Text != "" {
					fmt.Fprintf(&b, "[resource %s]\n%s\n", v.Resource.URI, v.Resource.Text)
				} else {
					fmt.Fprintf(&b, "[resource %s]\n", v.Resource.URI)
				}
			}
		default:
			data, _ := json.Marshal(c)
			b.Write(data)
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 && r.StructuredContent != nil {
		data, _ := json.Marshal(r.StructuredContent)
		b.Write(data)
	}
	s := strings.TrimSpace(b.String())
	if len(s) > resultMax {
		s = s[:resultMax] + "\n… cut"
	}
	if s == "" {
		s = "(no content)"
	}
	return s
}
