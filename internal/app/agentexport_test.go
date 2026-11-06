package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/agent/export"
)

func TestParseExportArgs(t *testing.T) {
	tests := []struct {
		in      string
		want    exportArgs
		wantErr bool
	}{
		{"", exportArgs{format: export.HTML}, false},
		{"--md", exportArgs{format: export.Markdown}, false},
		{"--jsonl --all out", exportArgs{format: export.JSONL, all: true, path: "out"}, false},
		{"notes/chat.md", exportArgs{format: export.Markdown, path: "notes/chat.md"}, false},
		{"--format=md my notes", exportArgs{format: export.Markdown, path: "my notes"}, false},
		{"--format jsonl", exportArgs{format: export.JSONL}, false},
		{"--html chat.md", exportArgs{format: export.HTML, path: "chat.md"}, false},
		{"--format pdf", exportArgs{}, true},
		{"--format", exportArgs{}, true},
		{"--zip", exportArgs{}, true},
	}
	for _, tt := range tests {
		got, err := parseExportArgs(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseExportArgs(%q) err %v", tt.in, err)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseExportArgs(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestExportPath(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	day := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	name := "hm-fix-it-2026-11-06.html"
	if got := exportPath("", export.HTML, "Fix it", day, root, home); got != filepath.Join(home, name) {
		t.Errorf("no Downloads folder: %s", got)
	}
	if err := os.Mkdir(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ in, want string }{
		{"", filepath.Join(home, "Downloads", name)},
		{"out", filepath.Join(root, "out", name)},
		{"chat", filepath.Join(root, "chat.html")},
		{"~/x.html", filepath.Join(home, "x.html")},
		{filepath.Join(home, "abs.html"), filepath.Join(home, "abs.html")},
	}
	for _, tt := range tests {
		if got := exportPath(tt.in, export.HTML, "Fix it", day, root, home); got != tt.want {
			t.Errorf("exportPath(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestSecretName(t *testing.T) {
	for name, want := range map[string]bool{"ANTHROPIC_API_KEY": true, "GITHUB_TOKEN": true, "aws_secret_access_key": true,
		"DB_PASSWORD": true, "HOME": false, "PATH": false, "KEYBOARD": false} {
		if got := secretName(name); got != want {
			t.Errorf("secretName(%s) = %v", name, got)
		}
	}
}

// exportHarness opens the agent with a session that mentions the
// Anthropic key hm keeps in the keychain.
func exportHarness(t *testing.T) (*harness, *agentRun, string) {
	t.Helper()
	h := newHarness(t, project(t), false)
	const key = "anthropic-secret-0123456789"
	h.app.agent.keys.Set("anthropic", key)
	h.app.agent.share.gitToken = func() string { return "" }
	h.app.agentOpen = true
	h.frame()
	r := h.app.run()
	r.session.Append(userMsg("Use the key "+key+" to call the API"), botMsg("Done."))
	h.app.notifs.items = nil
	return h, r, key
}

func lastNotif(h *harness) string {
	if n := len(h.app.notifs.items); n > 0 {
		return h.app.notifs.items[n-1].text
	}
	return ""
}

func TestExportCommand(t *testing.T) {
	h, r, key := exportHarness(t)
	var opened string
	h.app.agent.share.open = func(p string) error { opened = p; return nil }
	dir := t.TempDir()
	h.app.slashCommand(r, "/export "+dir)
	if r.errText != "" {
		t.Fatal(r.errText)
	}
	h.until("the export", 5*time.Second, func() bool { return opened != "" })
	if filepath.Dir(opened) != dir || filepath.Ext(opened) != ".html" {
		t.Fatalf("opened %s", opened)
	}
	data, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), key) || !strings.Contains(string(data), "[REDACTED: known secret]") {
		t.Fatal("the key was not redacted")
	}
	if strings.Contains(opened, key) {
		t.Fatalf("the file name holds the key: %s", opened)
	}
	if msg := lastNotif(h); !strings.Contains(msg, "Redacted 2 values: 2 known secret") {
		t.Fatalf("notification %q", msg)
	}

	// Markdown opens in the editor.
	md := filepath.Join(dir, "chat.md")
	h.app.slashCommand(r, "/export "+md)
	h.until("the Markdown export", 5*time.Second, func() bool { return h.app.bufs.Get(md) != nil })

	h.app.slashCommand(r, "/export --format pdf")
	if !strings.Contains(r.errText, "Unknown format") {
		t.Fatalf("errText %q", r.errText)
	}
}

func TestShareCommand(t *testing.T) {
	h, r, key := exportHarness(t)
	var uploads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Description string
			Public      bool
			Files       map[string]struct{ Content string }
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		if strings.Contains(body.Description, key) {
			t.Error("the gist's description holds the key")
		}
		tok := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		uploads = append(uploads, tok)
		for name, f := range body.Files {
			if strings.Contains(name+f.Content, key) || body.Public {
				t.Error("uploaded the key, or a public gist")
			}
		}
		if tok != "gist-token-0123" {
			w.Header().Set("X-OAuth-Scopes", "repo")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"g1","html_url":"https://gist.github.com/me/g1","owner":{"login":"me"}}`))
	}))
	defer srv.Close()
	h.app.agent.share.api = srv.URL
	h.app.agent.share.gitToken = func() string { return "repo-only-token-0123" }
	var asked string
	var yes func()
	h.app.agent.share.confirm = func(_, msg string, ok func()) { asked, yes = msg, ok }
	h.app.agent.share.askToken = func(ok func(string)) { ok("gist-token-0123") }
	h.app.setUserSetting([]string{"agent", "share", "viewer_url"}, "https://view.example/?g={id}")

	h.app.slashCommand(r, "/share")
	h.until("the confirmation", 5*time.Second, func() bool { return yes != nil })
	if !strings.Contains(asked, "secret gist") || !strings.Contains(asked, "Redacted 2 values") {
		t.Fatalf("asked %q", asked)
	}
	if len(uploads) != 0 {
		t.Fatal("uploaded before the user agreed")
	}
	yes()
	clip := h.app.ctx.Clipboard()
	h.until("the link", 5*time.Second, func() bool { return clip.Get() != "" })
	if got := clip.Get(); got != "https://view.example/?g=g1" {
		t.Fatalf("clipboard %q", got)
	}
	// The git token lacked the gist scope; the one asked for is kept.
	if strings.Join(uploads, ",") != "repo-only-token-0123,gist-token-0123" {
		t.Fatalf("uploads with %v", uploads)
	}
	if tok, _ := h.app.agent.keys.Get(gistKey); tok != "gist-token-0123" {
		t.Fatalf("saved token %q", tok)
	}
}
