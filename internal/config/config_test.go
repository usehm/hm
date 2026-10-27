package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsAreValid(t *testing.T) {
	cfg, ps := Load()
	if len(ps) > 0 {
		t.Fatalf("defaults have problems: %v", ps)
	}
	if cfg.Editor.Theme != "tokyo-night" || cfg.Keys["search.project"] != "cmd+f" {
		t.Fatalf("defaults not decoded: %+v", cfg.Editor)
	}
	if got := cfg.Modes["auto"].Tools; len(got) != 1 || got[0] != "all" {
		t.Fatalf("auto tools %v", got)
	}
	if cfg.Editor.AutoSave != AutoSaveOff || cfg.Editor.AutoSaveDelay.Duration != time.Second {
		t.Fatalf("auto save %+v", cfg.Editor)
	}
}

func TestLayersMergeLaterWins(t *testing.T) {
	user := Layer{Name: "user", Data: []byte("editor:\n  theme: nord\n  font_size: 16\nkeys:\n  file.save: cmd+alt+w\n")}
	proj := Layer{Name: "project", Data: []byte(`
editor:
  font_size: 12
search:
  exclude: [build]
languages:
  zig:
    extensions: [.zig]
    lsp: { command: zls }
`)}
	cfg, ps := Load(user, proj)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if cfg.Editor.Theme != "nord" || cfg.Editor.FontSize != 12 {
		t.Fatalf("editor %+v", cfg.Editor)
	}
	if cfg.Keys["file.save"] != "cmd+alt+w" || cfg.Keys["palette"] != "cmd+shift+p" {
		t.Fatal("keys should merge key by key")
	}
	if len(cfg.Search.Exclude) != 1 {
		t.Fatalf("lists replace: %v", cfg.Search.Exclude)
	}
	if cfg.Languages["zig"].LSP.Command != "zls" || cfg.Languages["go"].LSP == nil {
		t.Fatal("new language added next to the built-ins")
	}
}

func TestProblemsHavePositions(t *testing.T) {
	data := `editor:
  thme: nord
  font_size: big
  auto_save: sometimes
keys:
  file.save: cmd+nope+s
`
	_, ps := Load(Layer{Name: "u.yaml", Data: []byte(data)})
	want := map[string]int{"editor.thme": 2, "editor.font_size": 3, "editor.auto_save": 4, "keys.file.save": 6}
	got := map[string]Problem{}
	for _, p := range ps {
		got[p.Key] = p
	}
	for k, line := range want {
		p, ok := got[k]
		if !ok || p.Line != line || p.File != "u.yaml" {
			t.Errorf("%s: %+v (all %v)", k, p, ps)
		}
	}
	if !strings.Contains(got["editor.thme"].Msg, `did you mean "theme"`) {
		t.Errorf("suggestion missing: %s", got["editor.thme"].Msg)
	}
}

func TestSyntaxErrorLine(t *testing.T) {
	_, ps := Load(Layer{Name: "u.yaml", Data: []byte("editor:\n  theme: nord\n bad: [\n")})
	if len(ps) != 1 || ps[0].Line == 0 {
		t.Fatalf("problems %+v", ps)
	}
}

func TestUnknownTheme(t *testing.T) {
	_, ps := Load(Layer{Name: "u", Data: []byte("editor: { theme: tokyo-nigth }\n")})
	if len(ps) != 1 || !strings.Contains(ps[0].Msg, "tokyo-night") {
		t.Fatalf("%v", ps)
	}
}

func TestSetValueKeepsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := `# my settings
editor:
  theme: nord # cool
  # bigger is nicer
  font_size: 16
keys:
  file.save: cmd+s
`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetValue(path, []string{"editor", "theme"}, "dracula"); err != nil {
		t.Fatal(err)
	}
	if err := SetValue(path, []string{"terminal", "font_size"}, 15); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)
	for _, want := range []string{"# my settings", "theme: dracula # cool", "# bigger is nicer", "font_size: 16", "terminal:\n  font_size: 15"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Index(s, "editor:") > strings.Index(s, "keys:") {
		t.Error("key order changed")
	}
	if err := Unset(path, []string{"editor", "theme"}); err != nil {
		t.Fatal(err)
	}
	cfg, ps := Load(mustLayer(t, path))
	if len(ps) > 0 || cfg.Editor.Theme != "tokyo-night" || cfg.Terminal.FontSize != 15 {
		t.Fatalf("after unset: %+v %v", cfg.Editor, ps)
	}
}

func TestSetValueCreatesFile(t *testing.T) {
	old := UserDir
	UserDir = func() string { return "/x" }
	defer func() { UserDir = old }()
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := SetValue(path, []string{"editor", "soft_wrap"}, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "yaml-language-server: $schema=") || !strings.Contains(string(got), "soft_wrap: true") {
		t.Fatalf("new file:\n%s", got)
	}
}

func mustLayer(t *testing.T, p string) Layer {
	t.Helper()
	l, err := ReadLayer(p)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestManagerKeepsLastGoodAndHotReloads(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user", "config.yaml")
	root := filepath.Join(dir, "proj")
	_ = os.MkdirAll(root, 0o755)
	proj := ProjectPath(root)
	if err := os.MkdirAll(filepath.Dir(user), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(user, []byte("editor: { theme: nord }\n"), 0o644)

	m := NewManager(user, proj)
	defer m.Close()
	woke := make(chan struct{}, 8)
	if err := m.Watch(func() { woke <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	if m.Current().Editor.Theme != "nord" {
		t.Fatal("initial load")
	}

	_ = os.WriteFile(user, []byte("editor: { theme: [broken\n"), 0o644)
	waitWake(t, m, woke)
	if applied, ps := m.Reload(); applied || len(ps) == 0 {
		t.Fatal("broken file must not apply")
	}
	if m.Current().Editor.Theme != "nord" {
		t.Fatal("last good config must stay")
	}

	// The project file appears, .hm folder and all.
	_ = os.WriteFile(user, []byte("editor: { theme: nord }\n"), 0o644)
	_ = os.MkdirAll(filepath.Dir(proj), 0o755)
	time.Sleep(50 * time.Millisecond)
	_ = os.WriteFile(proj, []byte("editor: { theme: dracula }\n"), 0o644)
	waitWake(t, m, woke)
	if applied, ps := m.Reload(); !applied {
		t.Fatal(ps)
	}
	if m.Current().Editor.Theme != "dracula" {
		t.Fatalf("project wins: %s", m.Current().Editor.Theme)
	}
}

func waitWake(t *testing.T, m *Manager, woke chan struct{}) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-woke:
			if m.TakeChange() {
				return
			}
		case <-deadline:
			t.Fatal("no change noticed")
		}
	}
}

func TestSchemaIsJSON(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal(Schema(), &v); err != nil {
		t.Fatal(err)
	}
	ed := v["properties"].(map[string]any)["editor"].(map[string]any)["properties"].(map[string]any)
	if _, ok := ed["auto_save"].(map[string]any)["enum"]; !ok {
		t.Fatal("enum missing from schema")
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("HM_TOKEN", "abc")
	if v, err := Resolve("Bearer ${HM_TOKEN}"); err != nil || v != "Bearer abc" {
		t.Fatal(v, err)
	}
	if _, err := Resolve("${HM_NOPE_UNSET}"); err == nil {
		t.Fatal("unset env should fail")
	}
	Keychain = func(n string) (string, error) { return "k-" + n, nil }
	defer func() { Keychain = nil }()
	if v, _ := Resolve("keychain:anthropic"); v != "k-anthropic" {
		t.Fatal(v)
	}
}

func TestChordsParse(t *testing.T) {
	for _, k := range []string{"cmd+shift+p", "ctrl+`", "cmd+,", "cmd+alt+f", "cmd+f"} {
		if _, err := ParseShortcut(k); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
}

func TestLanguageFilesAddLanguages(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "config.yaml")
	langs := filepath.Join(dir, "languages")
	if err := os.MkdirAll(langs, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(langs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zig.yaml", "extensions: [.zig]\nlsp: { command: zls }\n")
	write("go.yaml", "highlight_queries: [queries/go.scm]\n")
	m := NewManager(user, "")
	cfg := m.Current()
	if z := cfg.Languages["zig"]; len(z.Extensions) != 1 || z.LSP == nil || z.LSP.Command != "zls" {
		t.Fatalf("zig from its file: %+v", z)
	}
	if g := cfg.Languages["go"]; g.LSP == nil || g.LSP.Command != "gopls" || len(g.HighlightQueries) != 1 {
		t.Fatalf("go merged with the defaults: %+v", g)
	}

	write("bad.yaml", "extensionz: [.x]\n")
	if _, ps := m.Reload(); len(ps) == 0 || !strings.Contains(ps[0].Error(), "bad.yaml") || ps[0].Line != 1 {
		t.Fatalf("problems %v, want one in bad.yaml at line 1", ps)
	}
}
