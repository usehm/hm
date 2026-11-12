package gitui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usehm/hm/internal/gitx"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repoWithRemote is a work tree with one commit, pushed to a bare remote.
func repoWithRemote(t *testing.T) (work, bare string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Keep the user's global config (signing, hooks) out of the test.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	base := t.TempDir()
	bare, work = filepath.Join(base, "remote.git"), filepath.Join(base, "work")
	run(t, base, "init", "--bare", "-b", "main", bare)
	run(t, base, "init", "-b", "main", work)
	run(t, work, "config", "user.name", "T")
	run(t, work, "config", "user.email", "t@x")
	write(t, filepath.Join(work, "a.go"), "package a\n\nfunc A() int { return 1 }\n")
	run(t, work, "add", ".")
	run(t, work, "commit", "-m", "init")
	run(t, work, "remote", "add", "origin", bare)
	run(t, work, "push", "-u", "origin", "main")
	return work, bare
}

func newTestGit(t *testing.T, dir string) (*Git, *[]string) {
	var errs []string
	g := NewGitPage(GitDeps{
		Dir:      func() (string, error) { return dir, nil },
		Error:    func(err error) { errs = append(errs, err.Error()) },
		Toast:    func(string) {},
		HasDirty: func() bool { return false },
	})
	return g, &errs
}

// settle runs results until the page is idle.
func settle(t *testing.T, g *Git) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		g.applyPending()
		if !g.loading && g.busy == "" {
			g.mu.Lock()
			n := len(g.pending)
			g.mu.Unlock()
			if n == 0 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("git page never settled")
}

func TestStageCommitPush(t *testing.T) {
	work, bare := repoWithRemote(t)
	write(t, filepath.Join(work, "a.go"), "package a\n\nfunc A() int { return 2 }\n")
	write(t, filepath.Join(work, "b.go"), "package a\n")
	write(t, filepath.Join(work, "c.go"), "package a\n")
	g, errs := newTestGit(t, work)
	g.Refresh()
	settle(t, g)
	if len(g.snap.entries) != 3 {
		t.Fatalf("changes %+v", g.snap.entries)
	}
	g.setIncluded("c.go", false) // leave c.go out of the commit
	g.message = "change a, add b"
	g.commit()
	settle(t, g)
	settle(t, g)
	if len(*errs) > 0 {
		t.Fatal(*errs)
	}
	if got := run(t, work, "log", "-1", "--format=%s"); got != "change a, add b" {
		t.Fatalf("head %q", got)
	}
	if st := run(t, work, "status", "--porcelain"); st != "?? c.go" {
		t.Fatalf("status %q", st)
	}
	if g.snap.status.Ahead != 1 {
		t.Fatalf("ahead %d", g.snap.status.Ahead)
	}
	g.push()
	settle(t, g)
	settle(t, g)
	if len(*errs) > 0 {
		t.Fatal(*errs)
	}
	if got := run(t, bare, "log", "-1", "--format=%s", "main"); got != "change a, add b" {
		t.Fatalf("remote head %q", got)
	}
}

func TestMergeConflictResolve(t *testing.T) {
	work, _ := repoWithRemote(t)
	run(t, work, "checkout", "-b", "feature")
	write(t, filepath.Join(work, "a.go"), "package a\n\nfunc A() int { return 10 }\n")
	run(t, work, "commit", "-am", "feature")
	run(t, work, "checkout", "main")
	write(t, filepath.Join(work, "a.go"), "package a\n\nfunc A() int { return 20 }\n")
	run(t, work, "commit", "-am", "main")

	g, errs := newTestGit(t, work)
	g.Refresh()
	settle(t, g)
	g.merge("feature")
	settle(t, g)
	settle(t, g)
	if g.snap.op != gitx.OpMerge || g.conflicts() != 1 {
		t.Fatalf("op %v conflicts %d errs %v", g.snap.op, g.conflicts(), *errs)
	}
	data, _ := os.ReadFile(filepath.Join(work, "a.go"))
	if !hasConflictMarkers(data) {
		t.Fatalf("expected markers:\n%s", data)
	}
	// Marking resolved with markers left is refused.
	e := findEntry(g.changes.entries, "a.go")
	g.markResolved(*e)
	if len(*errs) != 1 || !strings.Contains((*errs)[0], "conflict markers") {
		t.Fatalf("errs %v", *errs)
	}
	// Fix the file by hand, then mark it resolved and finish.
	write(t, filepath.Join(work, "a.go"), "package a\n\nfunc A() int { return 30 }\n")
	g.markResolved(*e)
	settle(t, g)
	settle(t, g)
	if g.conflicts() != 0 {
		t.Fatal("still conflicted")
	}
	g.finish()
	settle(t, g)
	settle(t, g)
	if g.snap.op != gitx.OpNone {
		t.Fatalf("op %v errs %v", g.snap.op, *errs)
	}
	if got := run(t, work, "show", "HEAD:a.go"); !strings.Contains(got, "return 30") {
		t.Fatalf("merged %q", got)
	}
}
