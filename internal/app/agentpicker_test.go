package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
)

// twoProviders is a workspace with an Anthropic provider (claude) and an
// OpenAI-compatible one (router) served by the same fake model, so a
// session can switch between them.
func twoProviders(t *testing.T, model *fakeModel, extra string) *harness {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"router-a"},{"id":"router-b"},{"id":"shared"}]}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"claude-sonnet-4-6","type":"model"},{"id":"claude-haiku-4-5","type":"model"}],"has_more":false}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/"):
			model.openai(w, r)
		default:
			model.anthropic(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	root := project(t)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), fmt.Sprintf(`
agent:
  default_provider: claude
  providers:
    claude: { type: anthropic, base_url: %q, api_key: "keychain:claude", model: claude-sonnet-4-6, effort: high }
    router: { type: openrouter, base_url: %q, api_key: "keychain:router", model: router-a }
%s`, srv.URL, srv.URL+"/api/v1", extra))
	h := newHarness(t, root, false)
	h.app.agent.keys.Set("claude", "sk-a")
	h.app.agent.keys.Set("router", "sk-b")
	h.app.refreshProviderKeys()
	h.app.agentOpen = true
	h.until("keys read", 10*time.Second, func() bool {
		return h.app.providerReady("claude") && h.app.providerReady("router")
	})
	return h
}

func waitTurn(t *testing.T, h *harness, text string) {
	t.Helper()
	runTask(t, h, text)
	h.until("turn end", 10*time.Second, func() bool { return !h.app.run().running })
	if e := h.app.run().errText; e != "" {
		t.Fatal(e)
	}
}

// messagesOf returns a request's messages as JSON, for checks.
func messagesOf(body map[string]any) string {
	var b strings.Builder
	fmt.Fprint(&b, body["messages"])
	return b.String()
}

func TestSwitchModelsMidSession(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{
		{{"read", `{"path":"main.go"}`}}, nil, // claude
		{{"read", `{"path":"go.mod"}`}}, nil, // router
		nil, // claude again
	}}
	h := twoProviders(t, model, "")
	r := h.app.run()
	waitTurn(t, h, "read main.go")

	h.app.slashCommand(r, "/model router/router-b")
	if r.provider != "router" || r.model != "router-b" || h.app.modelLabel(r) != "router-b" {
		t.Fatalf("after /model: %s %s %q", r.provider, r.model, h.app.modelLabel(r))
	}
	waitTurn(t, h, "now read go.mod")
	req := model.requests[2]
	if req["model"] != "router-b" || req["_path"] != nil {
		t.Fatalf("router request model %v", req["model"])
	}
	// The history goes along: Claude's tool call and its result, paired.
	if msgs := messagesOf(req); !strings.Contains(msgs, "toolu_1_0") || !strings.Contains(msgs, "tool_call_id:toolu_1_0") {
		t.Fatalf("history sent to router: %s", msgs)
	}

	h.app.slashCommand(r, "/model claude")
	if r.provider != "claude" || r.model != "" {
		t.Fatalf("back to claude: %s %q", r.provider, r.model)
	}
	waitTurn(t, h, "sum up")
	req = model.requests[4]
	if req["model"] != "claude-sonnet-4-6" {
		t.Fatalf("claude request model %v", req["model"])
	}
	// The router's call ids come back as tool_use and tool_result ids.
	if msgs := messagesOf(req); !strings.Contains(msgs, "id:call_3_0") || !strings.Contains(msgs, "tool_use_id:call_3_0") {
		t.Fatalf("history sent back to claude: %s", msgs)
	}

	// Each switch is in the session, and a reopened session goes on with
	// the model it last ran.
	s := r.session
	if s.Provider != "claude" || s.Model != "claude-sonnet-4-6" {
		t.Fatalf("session %s %s", s.Provider, s.Model)
	}
	h.app.slashCommand(r, "/model router/router-b")
	waitTurn(t, h, "one more")
	again := h.app.newRun(s)
	if again.provider != "router" || again.model != "router-b" {
		t.Fatalf("reopened on %s %q", again.provider, again.model)
	}
}

func TestResolveModel(t *testing.T) {
	h := twoProviders(t, &fakeModel{}, `
    other: { type: openrouter, base_url: "http://localhost:1/v1", model: shared }
`)
	r := h.app.run()
	h.until("model lists", 10*time.Second, func() bool { return !h.app.loadModelLists() })
	cases := []struct{ q, want, err string }{
		{"router/anything-at-all", "router/anything-at-all", ""},
		{"claude", "claude/claude-sonnet-4-6", ""},
		{"router-b", "router/router-b", ""},
		{"ROUTER-B", "router/router-b", ""},
		{"shared", "", "shared matches"}, // router and other both run it
		{"router/shared", "router/shared", ""},
		{"router-", "", "router- matches router/router-a, router/router-b"},
		{"nothing-like-it", "", "No model matches"},
	}
	for _, tc := range cases {
		m, err := h.app.resolveModel(r, tc.q)
		switch {
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%q: error %v, want %q", tc.q, err, tc.err)
		case tc.err == "" && (err != nil || m.String() != tc.want):
			t.Errorf("%q = %s %v, want %s", tc.q, m, err, tc.want)
		}
	}
	// Among several, the session's provider wins.
	r.provider = "other"
	if m, err := h.app.resolveModel(r, "shared"); err != nil || m.prov != "other" {
		t.Errorf("shared from other: %s %v", m, err)
	}
}

func TestModelPickerAndFavourites(t *testing.T) {
	h := twoProviders(t, &fakeModel{}, `
  favorite_models: [router/router-b, claude/claude-sonnet-4-6, nobody/x]
`)
	writeFile(t, filepath.Join(h.root, ".hm", "models.json"),
		`[{"id": "local-x", "provider": "router", "name": "Local X", "context_window": 12345}]`)
	h.app.agent.registry = nil
	r := h.app.run()

	h.press(input.KeyL, input.ModCtrl)
	if !h.c.Commands().Open || !h.app.agent.picking {
		t.Fatal("ctrl+l did not open the model picker")
	}
	h.until("model lists", 10*time.Second, func() bool { return !h.app.loadModelLists() })
	items := h.app.modelItems(h.c)
	var titles []string
	for _, it := range items {
		titles = append(titles, it.DisplayTitle())
	}
	got := strings.Join(titles, " ")
	// Favourites first, then each provider's models; models.json adds one.
	if !strings.HasPrefix(got, "router-b claude-sonnet-4-6 claude-haiku-4-5 router-a") || !strings.Contains(got, "local-x") {
		t.Fatalf("picker lists %s", got)
	}
	if d := modelDetail("Router", h.app.modelRegistry().Listed("router")[0]); d != "Router · Local X · 12.3k context" {
		t.Fatalf("detail %q", d)
	}
	if !h.c.Commands().Run("model.router/local-x") || r.provider != "router" || r.model != "local-x" {
		t.Fatalf("picking local-x: %s %q", r.provider, r.model)
	}
	if w := h.app.agentHost().ContextWindowOf(r.provider, r.model); w != 12345 {
		t.Fatalf("window of local-x %d", w)
	}
	h.c.Commands().Hide()
	h.frame()
	if h.app.agent.picking || len(h.app.modelItems(h.c)) != 0 {
		t.Fatal("picker entries stay after it closed")
	}

	// ctrl+p steps through the favourites whose provider is set up.
	var seen []string
	for range 3 {
		h.press(input.KeyP, input.ModCtrl)
		seen = append(seen, r.provider+"/"+h.app.runModel(r))
	}
	if got := strings.Join(seen, " "); got != "router/router-b claude/claude-sonnet-4-6 router/router-b" {
		t.Fatalf("ctrl+p went %s", got)
	}
}

func TestThink(t *testing.T) {
	model := &fakeModel{}
	h := twoProviders(t, model, "")
	r := h.app.run()
	if got := h.app.modelLabel(r); got != "claude-sonnet-4-6 · high" {
		t.Fatalf("label %q", got)
	}
	cases := []struct{ arg, effort, err string }{
		{"low", "low", ""},
		{"lots", "low", "Unknown effort lots"},
		{"off", "off", ""},
		{"default", "", ""},
		{"max", "max", ""},
	}
	for _, tc := range cases {
		r.errText = ""
		h.app.slashCommand(r, strings.TrimSpace("/think "+tc.arg))
		if r.effort != tc.effort || !strings.Contains(r.errText, tc.err) || (tc.err == "" && r.errText != "") {
			t.Errorf("/think %s: effort %q error %q", tc.arg, r.effort, r.errText)
		}
	}
	if got := h.app.modelLabel(r); got != "claude-sonnet-4-6 · max" {
		t.Fatalf("label %q", got)
	}
	waitTurn(t, h, "think hard")
	if oc, _ := model.requests[0]["output_config"].(map[string]any); oc["effort"] != "max" {
		t.Fatalf("effort sent %v", model.requests[0]["output_config"])
	}
	if r.session.Effort != "max" {
		t.Fatalf("session effort %q", r.session.Effort)
	}
	if again := h.app.newRun(r.session); again.effort != "max" {
		t.Fatalf("reopened with effort %q", again.effort)
	}

	// A model that lists its efforts takes only those.
	writeFile(t, filepath.Join(h.root, ".hm", "models.json"),
		`[{"id": "claude-sonnet-4-6", "efforts": ["low", "high"]}]`)
	h.app.agent.registry = nil
	h.app.slashCommand(r, "/think medium")
	if r.errText != "claude-sonnet-4-6 takes low, high" {
		t.Fatalf("medium on a low/high model: %q", r.errText)
	}
}
