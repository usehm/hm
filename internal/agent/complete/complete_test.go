package complete

import (
	"context"
	"strings"
	"testing"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

func TestEligible(t *testing.T) {
	cases := []struct {
		text  string
		caret int
		want  bool
	}{
		{"fmt.Pr", 6, true},
		{"f(x)", 3, true},           // only ")" follows
		{"foo(bar)", 4, false},      // code follows
		{"a\n  \nb", 4, true},       // indented empty line
		{"   ", 3, false},           // empty document
		{"x := 1 // c", 2, false},   // mid-line
		{"call(a, b);\n", 10, true}, // before ";"
	}
	for _, c := range cases {
		if got := Eligible([]byte(c.text), c.caret); got != c.want {
			t.Errorf("Eligible(%q, %d) = %v, want %v", c.text, c.caret, got, c.want)
		}
	}
}

func TestClean(t *testing.T) {
	cases := []struct {
		name, out, before, after, want string
	}{
		{"plain", "intln(x)", "fmt.Pr", "", "intln(x)"},
		{"fenced", "```go\nintln(x)\n```", "fmt.Pr", "", "intln(x)"},
		{"repeats line", "fmt.Println(x)", "\tfmt.Pr", "", "intln(x)"},
		{"closer exists", "x, y)", "f(", ")\n", "x, y"},
		{"marker", "a" + Cursor, "", "", "a"},
		{"empty", "  \n", "x", "", ""},
		{"indented blank line", "\treturn x\n}", "{\n\t", "", "return x\n}"},
	}
	for _, c := range cases {
		if got := Clean(c.out, c.before, c.after); got != c.want {
			t.Errorf("%s: Clean = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPromptWindowsLargeFiles(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	text := []byte(strings.Repeat(line, 500))
	caret := len(text) / 2
	p := Prompt(Request{Path: "a/b.go", Text: text, Caret: caret})
	if !strings.HasPrefix(p, "File: a/b.go\n\n") || !strings.Contains(p, Cursor) {
		t.Fatalf("prompt header or cursor missing: %q", p[:40])
	}
	if len(p) > maxBefore+maxAfter+100 {
		t.Fatalf("prompt too long: %d", len(p))
	}
	body := strings.TrimPrefix(p, "File: a/b.go\n\n")
	if !strings.HasPrefix(body, "x") {
		t.Fatalf("window should start at a line boundary: %q", body[:10])
	}
}

type fakeProvider struct {
	reply string
	got   provider.Request
}

func (f *fakeProvider) Name(string) string                       { return "fake" }
func (f *fakeProvider) Models(context.Context) ([]string, error) { return nil, nil }
func (f *fakeProvider) Stream(_ context.Context, req provider.Request, _ func(provider.Event)) (provider.Response, error) {
	f.got = req
	return provider.Response{Message: provider.Message{Role: provider.Assistant,
		Blocks: []provider.Block{{Type: provider.Text, Text: f.reply}}}}, nil
}

func TestAsk(t *testing.T) {
	p := &fakeProvider{reply: "```\nfmt.Println(err)\n```"}
	got, err := Ask(context.Background(), p, Request{Path: "m.go", Text: []byte("if err != nil {\n\tfmt.P\n}"), Caret: 22, Model: "small", MaxTokens: 64})
	if err != nil {
		t.Fatal(err)
	}
	if got != "rintln(err)" {
		t.Fatalf("Ask = %q", got)
	}
	if p.got.Model != "small" || p.got.MaxTokens != 64 || !strings.Contains(p.got.Messages[0].TextOf(), "fmt.P"+Cursor+"\n}") {
		t.Fatalf("request: %+v", p.got)
	}
}
