package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/gitx"
	"github.com/usehm/hm/internal/update"
	"github.com/usehm/hm/internal/version"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitProject is project() committed to a fresh repository.
func gitProject(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := project(t)
	gitRun(t, root, "init", "-b", "main")
	gitRun(t, root, "config", "user.name", "T")
	gitRun(t, root, "config", "user.email", "t@x")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "init")
	return root
}

func TestFooterBranchAndGutterMarks(t *testing.T) {
	root := gitProject(t)
	h := newHarness(t, root, false)
	h.until("git status", 10*time.Second, func() bool { return h.app.gitst.status != nil && h.app.gitst.repo != nil })
	if h.app.gitst.status.Branch != "main" {
		t.Fatalf("branch %q", h.app.gitst.status.Branch)
	}
	main := filepath.Join(root, "main.go")
	h.app.openFile(main)
	ed := h.app.ws.ActiveEditor()
	ed.ApplyText("package main\n\n// added\nfunc main() { println(1) }\n")
	h.until("change bars", 10*time.Second, func() bool {
		b := h.app.gitst.bases[main]
		return b != nil && b.marks != nil && b.marks[2] == ui.LineModified && b.marks[3] == ui.LineAdded
	})
	// Saving shows up in the footer's changed count.
	h.app.writeFile(main)
	h.until("dirty count", 10*time.Second, func() bool {
		return h.app.gitst.status != nil && len(h.app.gitst.status.Changes) == 1
	})
	h.app.openGit()
	h.frame()
}

func TestDiskChangesReloadCleanBuffers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("watcher timing")
	}
	root := gitProject(t)
	h := newHarness(t, root, false)
	main := filepath.Join(root, "main.go")
	h.app.openFile(main)
	h.frame()
	writeFile(t, main, "package main\n\nfunc main() { println(\"from disk\") }\n")
	writeFile(t, filepath.Join(root, "new.go"), "package main\n")
	h.until("reload from disk", 5*time.Second, func() bool {
		return strings.Contains(string(h.app.ws.ActiveEditor().Bytes()), "from disk")
	})
	if h.app.ws.ActiveEditor().Modified() {
		t.Fatal("a reloaded buffer is not dirty")
	}
	// A git checkout rewrites files: the buffer follows.
	gitRun(t, root, "checkout", "--", "main.go")
	h.until("checkout reload", 5*time.Second, func() bool {
		return !strings.Contains(string(h.app.ws.ActiveEditor().Bytes()), "from disk")
	})
	// Edited buffers are kept.
	ed := h.app.ws.ActiveEditor()
	ed.ApplyText("package main // mine\n")
	writeFile(t, main, "package main // theirs\n")
	h.until("changed-on-disk notice", 5*time.Second, func() bool { return hasNotification(h, "main.go changed on disk") })
	if !strings.Contains(string(ed.Bytes()), "mine") {
		t.Fatal("unsaved edits were overwritten")
	}
}

func TestUpdateAvailableNotification(t *testing.T) {
	name, ok := update.AssetName("v9.9.9", runtime.GOOS, runtime.GOARCH)
	if !ok {
		t.Skip("no release asset for this platform")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9", "html_url": "https://example.com/r",
			"assets": []map[string]any{
				{"name": name, "browser_download_url": "https://example.com/" + name, "size": 10},
				{"name": name + update.SigSuffix, "browser_download_url": "https://example.com/sig", "size": 1},
			},
		})
	}))
	defer srv.Close()
	updateLatestURL, version.Version = srv.URL, "v0.1.0"
	defer func() { updateLatestURL, version.Version = "", "dev" }()

	root := project(t)
	h := newHarness(t, root, false)
	h.app.checkForUpdate(h.app.invalidate)
	h.frame()
	s := h.app.upd.state()
	if s.phase != updateAvailable || s.release.Version != "v9.9.9" || s.asset != name {
		t.Fatalf("state %+v", s)
	}
	// The channel can switch the check off.
	h.app.upd = &updater{}
	h.app.setUserSetting([]string{"updates", "channel"}, "off")
	h.app.checkForUpdate(h.app.invalidate)
	if h.app.upd.state().phase != updateNone {
		t.Fatal("updates.channel: off should skip the check")
	}
}

func TestChangeKindsMarksFoldersWithTheirMostNotableChange(t *testing.T) {
	root := filepath.FromSlash("/w")
	st := &gitx.Status{Changes: []gitx.Change{
		{Path: "a/b/new.go", Kind: gitx.Untracked},
		{Path: "a/b/old.go", Kind: gitx.Modified},
		{Path: "a/c/x.go", Kind: gitx.Added},
		{Path: "top.go", Kind: gitx.Conflicted},
	}}
	got := changeKinds(root, st)
	want := map[string]gitx.ChangeKind{
		"a/b/new.go": gitx.Untracked,
		"a/b/old.go": gitx.Modified,
		"a/c/x.go":   gitx.Added,
		"top.go":     gitx.Conflicted,
		"a/b":        gitx.Modified,
		"a/c":        gitx.Added,
		"a":          gitx.Modified,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for rel, k := range want {
		if g, ok := got[filepath.Join(root, filepath.FromSlash(rel))]; !ok || g != k {
			t.Errorf("%s = %v (%v), want %v", rel, g, ok, k)
		}
	}
}
