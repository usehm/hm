package search

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, s := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func run(t *testing.T, engine, root string, q Query, opts Options) map[string][]Match {
	t.Helper()
	opts.Engine = engine
	got := map[string][]Match{}
	err := Run(context.Background(), root, q, opts, func(fr FileResult) bool {
		rel, _ := filepath.Rel(root, fr.Path)
		got[filepath.ToSlash(rel)] = fr.Matches
		return true
	})
	if err != nil && err != ErrTruncated {
		t.Fatalf("%s: %v", engine, err)
	}
	return got
}

func engines(t *testing.T) []string {
	es := []string{"builtin"}
	if _, err := exec.LookPath("rg"); err == nil {
		es = append(es, "ripgrep")
	} else {
		t.Log("rg not installed; testing the built-in engine only")
	}
	return es
}

var files = map[string]string{
	".gitignore":         "build/\n*.log\n!keep.log\n",
	"main.go":            "package main\n\nfunc main() { Hello(\"world\") }\n",
	"pkg/hello.go":       "package pkg\n\n// Hello says hello.\nfunc Hello(name string) {}\nfunc hello2() {}\n",
	"build/out.go":       "func Hello() {}\n",
	"debug.log":          "Hello\n",
	"keep.log":           "Hello\n",
	"node_modules/x.js":  "Hello\n",
	"docs/readme.md":     "say Hello\n",
	"pkg/sub/.gitignore": "gen.go\n",
	"pkg/sub/gen.go":     "Hello\n",
	"pkg/sub/real.go":    "Hello\n",
}

func keys(m map[string][]Match) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestEnginesAgreeAndHonorIgnores(t *testing.T) {
	root := tree(t, files)
	for _, e := range engines(t) {
		got := run(t, e, root, Query{Pattern: "Hello", Case: true}, Options{Exclude: []string{"node_modules"}})
		want := []string{"docs/readme.md", "keep.log", "main.go", "pkg/hello.go", "pkg/sub/real.go"}
		if k := keys(got); !equal(k, want) {
			t.Errorf("%s: files %v, want %v", e, k, want)
		}
		if ms := got["pkg/hello.go"]; len(ms) != 2 || ms[1].Line != 3 || ms[1].Col != 5 || ms[1].LineText != "func Hello(name string) {}" {
			t.Errorf("%s: matches %+v", e, ms)
		}
		got = run(t, e, root, Query{Pattern: "hello", Case: false, Word: true, Include: "*.go", Exclude: "pkg/sub"}, Options{})
		if k := keys(got); !equal(k, []string{"main.go", "pkg/hello.go"}) {
			t.Errorf("%s: include/exclude files %v", e, k)
		}
		if ms := got["pkg/hello.go"]; len(ms) != 3 { // "hello" in the comment twice, Hello once; hello2 is not a word match
			t.Errorf("%s: word matches %d %+v", e, len(ms), ms)
		}
	}
}

func TestRegexGroupsReplace(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "x := foo(1, 2)\ny := foo(3, 4)\n"})
	q := Query{Pattern: `foo\((\d+), (\d+)\)`, Regex: true, Case: true}
	re, _ := q.Compile()
	for _, e := range engines(t) {
		got := run(t, e, root, q, Options{})
		ms := got["a.go"]
		if len(ms) != 2 {
			t.Fatalf("%s: %+v", e, ms)
		}
		text, _ := os.ReadFile(filepath.Join(root, "a.go"))
		out, n := ReplaceMatches(re, true, text, ms[1:], "bar($2, $1)")
		if n != 1 || string(out) != "x := foo(1, 2)\ny := bar(4, 3)\n" {
			t.Fatalf("%s: replace one %q", e, out)
		}
	}
	text := []byte("a.b a.b")
	lit, _ := Query{Pattern: "a.b", Case: true}.Compile()
	if out, n := ReplaceAll(lit, false, text, "$1"); n != 2 || string(out) != "$1 $1" {
		t.Fatalf("literal replace %q", out)
	}
}

func TestOverlayAndDirtyBuffers(t *testing.T) {
	root := tree(t, map[string]string{"a.go": "old\n", "b.go": "nothing\n"})
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	buffers := map[string]string{a: "nothing here\n", b: "now old is here\n"}
	for _, e := range engines(t) {
		got := run(t, e, root, Query{Pattern: "old"}, Options{
			Overlay: func(p string) ([]byte, bool) { s, ok := buffers[p]; return []byte(s), ok },
			Dirty:   []string{a, b},
		})
		if k := keys(got); !equal(k, []string{"b.go"}) || got["b.go"][0].Col != 4 {
			t.Fatalf("%s: overlay results %v %+v", e, k, got)
		}
	}
}

func TestMaxResults(t *testing.T) {
	root := tree(t, map[string]string{"a.txt": "x\nx\nx\nx\n", "b.txt": "x\nx\n"})
	err := Run(context.Background(), root, Query{Pattern: "x"}, Options{Engine: "builtin", MaxResults: 3}, func(FileResult) bool { return true })
	if err != ErrTruncated {
		t.Fatal(err)
	}
}

func TestGlobs(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"*.go", "a/b/c.go", true},
		{"*.go", "a/b/c.gox", false},
		{"cmd/**", "cmd/hm/main.go", true},
		{"cmd/**", "x/cmd/main.go", false},
		{"**/testdata/**", "a/testdata/x", true},
		{"vendor", "a/vendor/x.go", true},
		{"/build", "build/x", true},
		{"/build", "a/build/x", false},
		{"f?o.[tj]s", "foo.ts", true},
	}
	for _, c := range cases {
		g, _ := compileGlob(c.glob)
		if got := g.match(c.path); got != c.want {
			t.Errorf("%s ~ %s = %v", c.glob, c.path, got)
		}
	}
	_ = regexp.MustCompile
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
