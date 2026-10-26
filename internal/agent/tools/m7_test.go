package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/config"
)

func TestCommandToolPassesArgvSafely(t *testing.T) {
	e := setup(t)
	t.Setenv("HM_TEST_DB", "postgres://secret@db")
	cfg := map[string]config.CommandTool{
		"echo_args": {
			Description: "print args",
			Command:     `printf '%s|' {msg} "fixed word" ${HM_TEST_DB}`,
			Params:      map[string]config.Param{"msg": {Type: "string", Required: true}},
			Timeout:     config.Duration{Duration: 5 * time.Second},
		},
	}
	ts, errs := CommandTools(cfg)
	if len(errs) > 0 || len(ts) != 1 {
		t.Fatal(errs)
	}
	plan, r := run(t, e, ts[0], `{"msg":"a; touch pwned ${HM_TEST_DB}"}`)
	if r.IsError {
		t.Fatal(r.Text)
	}
	want := "a; touch pwned ${HM_TEST_DB}|fixed word|postgres://secret@db|"
	if !strings.HasPrefix(r.Text, want) {
		t.Fatalf("output %q", r.Text)
	}
	if _, err := os.Stat(e.root + "/pwned"); err == nil {
		t.Fatal("parameter was run by a shell")
	}
	if strings.Contains(plan.Summary, "secret") || plan.Category != CatCommand {
		t.Fatalf("summary leaks the env value or wrong category: %q %q", plan.Summary, plan.Category)
	}
	if _, r := run(t, e, ts[0], `{}`); !r.IsError || !strings.Contains(r.Text, "required") {
		t.Fatalf("missing param: %q", r.Text)
	}
	var sch map[string]any
	_ = json.Unmarshal(ts[0].Spec().Schema, &sch)
	if req, _ := sch["required"].([]any); len(req) != 1 || req[0] != "msg" {
		t.Fatalf("schema %v", sch)
	}
}

func TestSplitArgs(t *testing.T) {
	got, err := SplitArgs(`migrate -path {dir} -database "${DATABASE_URL}" 'up' a\ b`)
	if err != nil || strings.Join(got, "|") != `migrate|-path|{dir}|-database|${DATABASE_URL}|up|a b` {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := SplitArgs(`echo "x`); err == nil {
		t.Fatal("unclosed quote")
	}
}

const ddgPage = `<html><body>
<div class="result results_links"><div class="links_main result__body">
<h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&amp;rut=x">Documentation - <b>Go</b></a></h2>
<a class="result__snippet" href="x">The <b>Go</b> programming language docs.</a>
</div></div>
<div class="result"><div class="result__body"><h2><a class="result__a" href="https://pkg.go.dev/">pkg.go.dev</a></h2>
<a class="result__snippet">Package docs</a></div></div>
</body></html>`

func TestWebSearchAndFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/html/":
			_ = r.ParseForm()
			if r.Form.Get("q") != "golang docs" {
				t.Errorf("query %q", r.Form.Get("q"))
			}
			w.Write([]byte(ddgPage))
		case "/searx/search":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"results":[{"title":"T","url":"https://x","content":"C"}]}`))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><head><script>evil()</script></head><body><nav>menu</nav><h1>Title</h1><p>Hello <a href="https://go.dev">Go</a>.</p><ul><li>one</li></ul></body></html>`))
		}
	}))
	defer srv.Close()
	rs, err := Search(context.Background(), srv.Client(), config.WebSearch{URL: srv.URL + "/html/"}, "golang docs")
	if err != nil || len(rs) != 2 || rs[0].URL != "https://go.dev/doc/" || rs[0].Title != "Documentation - Go" ||
		rs[0].Snippet != "The Go programming language docs." {
		t.Fatalf("ddg %+v %v", rs, err)
	}
	rs, err = Search(context.Background(), srv.Client(), config.WebSearch{Provider: "searxng", URL: srv.URL + "/searx"}, "q")
	if err != nil || len(rs) != 1 || rs[0].Snippet != "C" {
		t.Fatalf("searxng %+v %v", rs, err)
	}
	text, err := Fetch(context.Background(), srv.Client(), srv.URL+"/page")
	if err != nil || text != "# Title\nHello Go (https://go.dev) .\n- one" {
		t.Fatalf("fetch %q %v", text, err)
	}
	wf := &webFetchTool{http: srv.Client()}
	if _, err := wf.Prepare(nil, json.RawMessage(`{"url":"file:///etc/passwd"}`)); err == nil {
		t.Fatal("non-http URLs must be refused")
	}
}
