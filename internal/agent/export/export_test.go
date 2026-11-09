package export

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/agent"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const fixtureID = "20261105-100000-fixture00001"

// fixture loads testdata/session.jsonl: a session with thinking, an edit,
// a write, a failed shell call, a compaction, a label, secrets, and a
// second branch with a summary.
func fixture(t *testing.T) *agent.Session {
	t.Helper()
	data, err := os.ReadFile("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	dir := filepath.Join(state, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fixtureID+".jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := agent.NewStore(state).Load(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const knownSecret = "hm-internal-SECRET-value"

func TestGolden(t *testing.T) {
	tests := []struct {
		name string
		o    Options
	}{
		{"session.html", Options{Format: HTML}},
		{"session-all.html", Options{Format: HTML, AllBranches: true}},
		{"session.md", Options{Format: Markdown}},
		{"session-all.md", Options{Format: Markdown, AllBranches: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.o.Location = time.UTC
			tt.o.Redactor = NewRedactor(knownSecret)
			res, err := Render(fixture(t), tt.o)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", tt.name+".golden")
			if *update {
				if err := os.WriteFile(golden, res.Data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update)", err)
			}
			if !bytes.Equal(res.Data, want) {
				t.Errorf("output differs from %s; run go test -update and review the diff", golden)
			}
		})
	}
}

// TestNoSecretsOrRequests checks every format for the fixture's secrets,
// and the HTML page for anything that would load or run.
func TestNoSecretsOrRequests(t *testing.T) {
	secrets := []string{knownSecret, "sk-ant-api03-AAAAbbbbCCCCddddEEEEffff1234", "ghp_0123456789abcdefghijABCDEFGHIJ012345"}
	for _, f := range []Format{HTML, Markdown, JSONL} {
		for _, all := range []bool{false, true} {
			res, err := Render(fixture(t), Options{Format: f, AllBranches: all, Redactor: NewRedactor(knownSecret)})
			if err != nil {
				t.Fatal(err)
			}
			out := string(res.Data)
			for _, s := range secrets {
				if strings.Contains(out, s) {
					t.Errorf("%s (all %v) holds secret %q", f, all, s)
				}
			}
			got := map[string]int{}
			for _, r := range res.Redacted {
				got[r.Kind] = r.Count
			}
			if got["known secret"] == 0 || got["anthropic key"] == 0 || got["github token"] == 0 {
				t.Errorf("%s: redactions %v", f, res.Redacted)
			}
			if f != HTML {
				continue
			}
			for _, bad := range []string{"<script", "<link", "@import", "url("} {
				if strings.Contains(out, bad) {
					t.Errorf("HTML holds %q", bad)
				}
			}
			// Pictures are inline: every src is a data URL.
			if n, inline := strings.Count(out, "src="), strings.Count(out, `src="data:image/`); n != inline || n == 0 {
				t.Errorf("HTML has %d src attributes, %d of them inline images", n, inline)
			}
		}
	}
}

func TestBranches(t *testing.T) {
	res, err := Render(fixture(t), Options{Format: HTML})
	if err != nil {
		t.Fatal(err)
	}
	if out := string(res.Data); strings.Contains(out, "Explain greet.go instead") || strings.Contains(out, "Other branch") {
		t.Error("the active path's export shows another branch")
	}
	res, err = Render(fixture(t), Options{Format: HTML, AllBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Data)
	for _, want := range []string{"Other branch · 2 messages", "Explain greet.go instead", "Explained greet.go without changing it."} {
		if !strings.Contains(out, want) {
			t.Errorf("all branches: missing %q", want)
		}
	}
}

func TestJSONL(t *testing.T) {
	res, err := Render(fixture(t), Options{Format: JSONL, Redactor: NewRedactor(knownSecret)})
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(res.Data), []byte("\n"))
	types := map[string]int{}
	for _, l := range lines {
		var rec map[string]any
		if err := json.Unmarshal(l, &rec); err != nil {
			t.Fatalf("line %s: %v", l, err)
		}
		types[rec["type"].(string)]++
	}
	if types["header"] != 1 || types["state"] != 1 || types["entry"] != 13 || types["label"] != 1 || types["branch_summary"] != 1 {
		t.Errorf("record types %v", types)
	}
}

func TestRedact(t *testing.T) {
	r := NewRedactor("swordfish-2026", "short")
	tests := []struct {
		in, want, kind string
	}{
		{"key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123", "key [REDACTED: anthropic key]", "anthropic key"},
		{"OPENAI=sk-proj-abcdefghij0123456789KLMNOP", "OPENAI=[REDACTED: openai key]", "openai key"},
		{"ghp_abcdefghijklmnopqrstuvwxyz0123456789", "[REDACTED: github token]", "github token"},
		{"github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz", "[REDACTED: github token]", "github token"},
		{"id AKIAIOSFODNN7EXAMPLE.", "id [REDACTED: aws key].", "aws key"},
		{"xoxb-123456789012-abcdefghij", "[REDACTED: slack token]", "slack token"},
		{"AIzaSyA-abcdefghijklmnopqrstuvwxyz01234", "[REDACTED: google key]", "google key"},
		{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "[REDACTED: jwt]", "jwt"},
		{"Authorization: Bearer abc123def456ghi789jkl", "Authorization: Bearer [REDACTED: bearer token]", "bearer token"},
		{`"api_key": "a1b2c3d4e5f6g7h8"`, `"api_key": "[REDACTED: secret value]"`, "secret value"},
		{"PASSWORD=hunter2hunter2x", "PASSWORD=[REDACTED: secret value]", "secret value"},
		{"pass swordfish-2026 along", "pass [REDACTED: known secret] along", "known secret"},
		{"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----", "[REDACTED: private key]", "private key"},
	}
	for _, tt := range tests {
		got, rs := r.Redact(tt.in)
		if got != tt.want {
			t.Errorf("Redact(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if len(rs) != 1 || rs[0].Kind != tt.kind || rs[0].Count != 1 {
			t.Errorf("Redact(%q) reported %v, want one %s", tt.in, rs, tt.kind)
		}
	}
}

// TestRedactLeavesOrdinaryText guards against redacting things that look
// a little like keys.
func TestRedactLeavesOrdinaryText(t *testing.T) {
	r := NewRedactor("short", "")
	for _, s := range []string{
		"commit e746d3f9f3bf84c0112bc10ea0f90d4f58db199d",
		"uuid 123e4567-e89b-12d3-a456-426614174000",
		"disk-usage-report-generator-tool-2026",
		"scikit sk-learn-tutorial-for-beginners",
		"token: string",
		"password = os.Getenv(\"DB_PASSWORD\")",
		"tokens: 1200 in, 80 out",
		"a short word",
		"Bearer of bad news",
	} {
		if got, rs := r.Redact(s); got != s || len(rs) != 0 {
			t.Errorf("Redact(%q) = %q, %v", s, got, rs)
		}
	}
}

func TestDescribe(t *testing.T) {
	if got := Describe(nil); got != "" {
		t.Errorf("Describe(nil) = %q", got)
	}
	got := Describe([]Redaction{{"anthropic key", 2}, {"known secret", 1}})
	if want := "3 values: 2 anthropic key, 1 known secret"; got != want {
		t.Errorf("Describe = %q, want %q", got, want)
	}
}

func TestFileName(t *testing.T) {
	day := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		title string
		f     Format
		want  string
	}{
		{"Fix the login bug!", HTML, "hm-fix-the-login-bug-2026-11-06.html"},
		{"  ", Markdown, "hm-session-2026-11-06.md"},
		{"Ünïcode / path\\..", JSONL, "hm-n-code-path-2026-11-06.jsonl"},
		{strings.Repeat("abc ", 40), HTML, "hm-" + strings.Repeat("abc-", 12) + "ab-2026-11-06.html"},
	}
	for _, tt := range tests {
		if got := FileName(tt.title, day, tt.f); got != tt.want {
			t.Errorf("FileName(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"html": HTML, ".md": Markdown, "Markdown": Markdown, "jsonl": JSONL} {
		if got, ok := ParseFormat(in); !ok || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := ParseFormat("pdf"); ok {
		t.Error("pdf parsed")
	}
}
