// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package prompts

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSplit(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  a  b ", []string{"a", "b"}},
		{`"a b" c`, []string{"a b", "c"}},
		{`'it''s' x`, []string{"its", "x"}},
		{`'a "b"'`, []string{`a "b"`}},
		{`a\ b`, []string{"a b"}},
		{`""`, []string{""}},
		{`x"y z"`, []string{"xy z"}},
		{"a\nb", []string{"a", "b"}},
	} {
		if got := Split(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Split(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     Template
		err      bool
	}{
		{"plain", "Review it.\n", Template{Body: "Review it."}, false},
		{"front", "---\ndescription: Review a file\nargument-hint: <file>\nmode: lazy\nmodel: openai\nallowed-tools: Bash(git:*)\n---\nReview $1.\n",
			Template{Description: "Review a file", ArgumentHint: "<file>", Mode: "lazy", Model: "openai", Body: "Review $1."}, false},
		{"empty front", "---\n---\nHi", Template{Body: "Hi"}, false},
		{"front at end", "---\ndescription: d\n---", Template{Description: "d"}, false},
		{"crlf", "---\r\ndescription: d\r\n---\r\nHi\r\n", Template{Description: "d", Body: "Hi"}, false},
		{"unclosed", "---\ndescription: d\nHi", Template{}, true},
		{"bad yaml", "---\ndescription: [\n---\nHi", Template{}, true},
	} {
		got, err := parse(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("%s: err %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestExpand(t *testing.T) {
	files := map[string]string{"a.txt": "alpha\n", "src/b.go": "package b"}
	include := func(p string) (string, error) {
		if s, ok := files[p]; ok {
			return s, nil
		}
		return "", errors.New("not found")
	}
	for _, tc := range []struct {
		name, body, args, want string
		warnings               int
	}{
		{"arguments", "Do: $ARGUMENTS.", ` x  "y z" `, `Do: x  "y z".`, 0},
		{"positional", "$2 then $1", `one "two words"`, "two words then one", 0},
		{"missing positional", "[$1][$3]", "a", "[a][]", 0},
		{"unknown stays", "$HOME $0 $ cost $5x", "", "$HOME $0 $ cost x", 0},
		{"many", "$10", "1 2 3 4 5 6 7 8 9 ten", "ten", 0},
		{"include", "Look:\n@a.txt\nend", "", "Look:\nalpha\nend", 0},
		{"include arg", "@$1", "src/b.go", "package b", 0},
		{"include missing", "@nope.txt", "", "@nope.txt", 1},
		{"not a line", "mail me @a.txt", "", "mail me @a.txt", 0},
	} {
		got, w := Template{Body: tc.body}.Expand(tc.args, include)
		if got != tc.want || len(w) != tc.warnings {
			t.Errorf("%s: got %q %v, want %q with %d warnings", tc.name, got, w, tc.want, tc.warnings)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	user, project, claude := filepath.Join(dir, "user"), filepath.Join(dir, "project"), filepath.Join(dir, "claude")
	write(t, filepath.Join(user, "review.md"), "---\ndescription: user review\n---\nuser")
	write(t, filepath.Join(user, "fix.md"), "fix it")
	write(t, filepath.Join(project, "review.md"), "---\ndescription: project review\n---\nproject")
	write(t, filepath.Join(project, "notes.txt"), "not a template")
	write(t, filepath.Join(project, "broken.md"), "---\ndescription: x\n")
	write(t, filepath.Join(claude, "frontend", "component.md"), "---\nallowed-tools: Bash\ndescription: make one\n---\nMake $ARGUMENTS")
	write(t, filepath.Join(claude, "frontend", "fix.md"), "nested fix")
	write(t, filepath.Join(claude, "fix.md"), "top fix")

	got, errs := Load([]string{user, project, claude, filepath.Join(dir, "missing")})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "broken.md") {
		t.Errorf("errs %v", errs)
	}
	var names []string
	by := map[string]Template{}
	for _, tp := range got {
		names = append(names, tp.Name)
		by[tp.Name] = tp
	}
	if !reflect.DeepEqual(names, []string{"component", "fix", "review"}) {
		t.Fatalf("names %v", names)
	}
	if by["review"].Description != "project review" {
		t.Errorf("later folder should win: %+v", by["review"])
	}
	if by["fix"].Body != "top fix" {
		t.Errorf("top-level file should beat subfolder: %+v", by["fix"])
	}
	if c := by["component"]; c.Group != "frontend" || c.Description != "make one" || c.Path != filepath.Join(claude, "frontend", "component.md") {
		t.Errorf("component %+v", c)
	}
}

func TestDirs(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := Dirs([]string{"~/p", ".hm/prompts", "/abs"}, "/ws")
	want := []string{filepath.Join(home, "p"), filepath.Join("/ws", ".hm/prompts"), "/abs"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Dirs = %v, want %v", got, want)
	}
}
