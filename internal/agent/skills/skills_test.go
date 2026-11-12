// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package skills

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeLoadAndTool(t *testing.T) {
	builtin := filepath.Join(t.TempDir(), "builtin")
	if err := Materialize(builtin); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(builtin, "fullstack-go-shadcn-firebase-postgres", "scaffold.sh")); err != nil || st.Mode()&0o100 == 0 {
		t.Fatalf("scaffold.sh not written executable: %v", err)
	}
	user := t.TempDir()
	if err := os.MkdirAll(filepath.Join(user, "mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(user, "mine", "SKILL.md"), []byte("---\nname: mine\ndescription: My skill\n---\n\nDo it my way.\n"), 0o644)
	os.WriteFile(filepath.Join(user, "mine", "notes.txt"), []byte("note"), 0o644)
	os.MkdirAll(filepath.Join(user, "bad"), 0o755)
	os.WriteFile(filepath.Join(user, "bad", "SKILL.md"), []byte("---\nname: bad\n"), 0o644)

	list, errs := Load(Dirs(builtin, []string{user}, "/"))
	if len(errs) != 1 || len(list) != 2 {
		t.Fatalf("skills %+v errs %v", list, errs)
	}
	if list[1].Name != "mine" || list[1].Body != "Do it my way." {
		t.Fatalf("parsed %+v", list[1])
	}
	p := Prompt(list, []string{"fullstack-go-shadcn-firebase-postgres"})
	if !strings.Contains(p, "- mine: My skill") || !strings.Contains(p, "scaffold.sh") {
		t.Fatalf("prompt %s", p)
	}
	tool := Tool(list)
	plan, err := tool.Prepare(nil, json.RawMessage(`{"name":"mine"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r := plan.Run(context.Background()); !strings.Contains(r.Text, "notes.txt") {
		t.Fatalf("skill body %q", r.Text)
	}
	plan, _ = tool.Prepare(nil, json.RawMessage(`{"name":"mine","file":"../bad/SKILL.md"}`))
	if r := plan.Run(context.Background()); !r.IsError {
		t.Fatal("reading outside the skill folder must fail")
	}
}

// TestFullstackScaffoldBuilds scaffolds the fullstack template and builds
// the API and the admin. It needs the network (Go modules, npm), so it only
// runs with HM_TEST_FULLSTACK=1.
func TestFullstackScaffoldBuilds(t *testing.T) {
	if os.Getenv("HM_TEST_FULLSTACK") != "1" {
		t.Skip("set HM_TEST_FULLSTACK=1 to build the fullstack template")
	}
	builtin := filepath.Join(t.TempDir(), "builtin")
	if err := Materialize(builtin); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(t.TempDir(), "shop")
	run := func(dir string, name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run(builtin, "sh", filepath.Join(builtin, "fullstack-go-shadcn-firebase-postgres", "scaffold.sh"), app, "shop", "example.com/shop")
	run(filepath.Join(app, "backend"), "go", "build", "./...")
	run(filepath.Join(app, "backend"), "go", "vet", "./...")
	run(filepath.Join(app, "admin"), "npm", "install", "--no-fund", "--no-audit", "--loglevel=error")
	run(filepath.Join(app, "admin"), "npm", "run", "build")
}
