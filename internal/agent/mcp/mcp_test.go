// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usehm/hm/internal/config"
)

type echoIn struct {
	Text string `json:"text" jsonschema:"what to echo"`
}

// testServer has one tool, echo, which reports the server's env too.
func testServer() *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	sdk.AddTool(s, &sdk.Tool{Name: "echo", Description: "Echo text"},
		func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
			text := in.Text
			if tok := os.Getenv("HM_TEST_TOKEN"); tok != "" {
				text += " token=" + tok
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil, nil
		})
	return s
}

// TestMain doubles as the stdio server when HM_MCP_SERVE is set.
func TestMain(m *testing.M) {
	if os.Getenv("HM_MCP_SERVE") == "1" {
		_ = testServer().Run(context.Background(), &sdk.StdioTransport{})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func callEcho(t *testing.T, m *Manager, name string) string {
	t.Helper()
	for _, tl := range m.Tools() {
		if tl.Spec().Name != name {
			continue
		}
		plan, err := tl.Prepare(nil, json.RawMessage(`{"text":"hi"}`))
		if err != nil {
			t.Fatal(err)
		}
		if plan.Category != CatMCP {
			t.Fatalf("category %q", plan.Category)
		}
		r := plan.Run(context.Background())
		if r.IsError {
			t.Fatal(r.Text)
		}
		return r.Text
	}
	t.Fatalf("no tool %s in %v", name, m.Servers())
	return ""
}

func TestStdioAndHTTPServers(t *testing.T) {
	t.Setenv("HM_SECRET_FOR_TEST", "s3cret")
	srv := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return testServer() }, nil))
	defer srv.Close()
	m := NewManager(t.TempDir())
	defer m.Close()
	m.Apply(context.Background(), map[string]config.MCPServer{
		"local": {Transport: "stdio", Command: os.Args[0], Env: map[string]string{
			"HM_MCP_SERVE": "1", "HM_TEST_TOKEN": "${HM_SECRET_FOR_TEST}"}},
		"remote": {Transport: "http", URL: srv.URL, Approval: "auto"},
		"broken": {Transport: "stdio", Command: "hm-no-such-mcp-server"},
	})
	if got := callEcho(t, m, "mcp__local__echo"); got != "hi token=s3cret" {
		t.Fatalf("stdio echo %q", got)
	}
	if got := callEcho(t, m, "mcp__remote__echo"); got != "hi" {
		t.Fatalf("http echo %q", got)
	}
	var broken *Server
	for _, s := range m.Servers() {
		if s.Name == "broken" {
			s := s
			broken = &s
		}
	}
	if broken == nil || broken.Err == nil {
		t.Fatal("a missing server should report an error")
	}
	if a := m.Approvals(); a["mcp__remote__*"] != "auto" || len(a) != 1 {
		t.Fatalf("approvals %v", a)
	}
	spec := m.Tools()[0].Spec()
	if !strings.Contains(string(spec.Schema), `"text"`) {
		t.Fatalf("schema %s", spec.Schema)
	}
	// Dropping a server from the config closes it.
	m.Apply(context.Background(), map[string]config.MCPServer{"remote": {Transport: "http", URL: srv.URL, Approval: "auto"}})
	for _, tl := range m.Tools() {
		if strings.HasPrefix(tl.Spec().Name, "mcp__local__") {
			t.Fatal("removed server still offers tools")
		}
	}
}
