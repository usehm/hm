package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type issueIn struct {
	ID int `json:"id"`
}

func fakeIssueTracker() *httptest.Server {
	return httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		s := sdk.NewServer(&sdk.Implementation{Name: "tracker", Version: "1"}, nil)
		sdk.AddTool(s, &sdk.Tool{Name: "get_issue", Description: "Read an issue"},
			func(_ context.Context, _ *sdk.CallToolRequest, in issueIn) (*sdk.CallToolResult, any, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: fmt.Sprintf("Issue %d: main should print 1", in.ID)}}}, nil, nil
			})
		return s
	}, nil))
}

// An MCP server and a command tool come from YAML alone, and the agent can
// use both, plus the bundled skill.
func TestMCPAndCommandToolFromYAML(t *testing.T) {
	tracker := fakeIssueTracker()
	model := &fakeModel{turns: [][]toolCall{
		{{"mcp__tracker__get_issue", `{"id":7}`}, {"run_tests", `{"pkg":"./..."}`},
			{"skill", `{"name":"fullstack-go-shadcn-firebase-postgres"}`}},
		nil,
	}}
	h, root := agentProject(t, "openai", model)
	// The MCP client keeps a stream open; close it before the server.
	defer func() { h.app.agent.mcp.Close(); tracker.Close() }()
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), strings.Replace(readConfig(t, root), "agent:", fmt.Sprintf(`tools:
  mcp:
    tracker: { transport: http, url: %q, approval: auto }
  commands:
    run_tests:
      description: Run the Go tests
      command: "go vet {pkg}"
      params: { pkg: { type: string, default: ./... } }
      timeout: 120s
agent:`, tracker.URL), 1))
	h.app.reloadConfig()
	h.until("MCP tools", 10*time.Second, func() bool { return len(h.app.agent.mcp.Tools()) == 1 })

	runTask(t, h, "fix issue 7")
	h.until("turn end", 60*time.Second, func() bool { return !h.app.run().running })
	if h.app.run().errText != "" {
		t.Fatal(h.app.run().errText)
	}
	offered := fmt.Sprint(model.requests[0]["tools"])
	for _, name := range []string{"mcp__tracker__get_issue", "run_tests", "skill", "web_search", "lsp_definition"} {
		if !strings.Contains(offered, name) {
			t.Fatalf("%s not offered", name)
		}
	}
	if sys := fmt.Sprint(model.requests[0]["messages"]); !strings.Contains(sys, "fullstack-go-shadcn-firebase-postgres:") {
		t.Fatal("skill descriptions missing from the system prompt")
	}
	results := fmt.Sprint(model.requests[1]["messages"])
	for _, want := range []string{"Issue 7: main should print 1", "[exit status 0]", "scaffold.sh"} {
		if !strings.Contains(results, want) {
			t.Fatalf("missing %q in tool results:\n%s", want, results)
		}
	}
}

func readConfig(t *testing.T, root string) string {
	t.Helper()
	data, err := readFileErr(filepath.Join(root, ".hm", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAgentLSPTools(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	root := goProject(t)
	h := newHarness(t, root, false)
	code := agentCode{h.app}
	main := filepath.Join(root, "main.go")
	var err error
	var defs []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		locs, e := code.Definition(context.Background(), main, 5, 30)
		err = e
		for _, l := range locs {
			defs = append(defs, fmt.Sprintf("%s:%d:%d", filepath.Base(l.Path), l.Line, l.Col))
		}
	}()
	h.until("definition", 60*time.Second, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if err != nil || len(defs) != 1 || defs[0] != "strings.go:4:6" {
		t.Fatalf("definition %v %v", defs, err)
	}
	var hover string
	done = make(chan struct{})
	go func() { defer close(done); hover, err = code.Hover(context.Background(), main, 5, 30) }()
	h.until("hover", 60*time.Second, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if err != nil || !strings.Contains(hover, "Shout(s string) string") {
		t.Fatalf("hover %q %v", hover, err)
	}
	h.app.releaseAgentBuffers()
	if h.app.bufs.Get(main) != nil {
		t.Fatal("borrowed buffer not released")
	}
}
