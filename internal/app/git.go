package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/diff"
	"github.com/mirzakhany/hm/internal/gitui"
	"github.com/mirzakhany/hm/internal/gitx"
	"github.com/mirzakhany/hm/internal/workspace"
)

// gitPollInterval is how often git status is read while nothing on disk
// says it changed: commits made in a terminal touch only .git.
const gitPollInterval = 5 * time.Second

// gitState is the open folder's repository as the shell sees it.
type gitState struct {
	page    *gitui.Git
	status  *gitx.Status
	repo    *gitx.Repo // nil until opened, or when the folder is not a repository
	head    string
	polled  time.Time
	stop    chan struct{}
	bases   map[string]*gitBase // HEAD versions of open files, for change bars
	watcher *workspace.Watcher
}

// gitBase is a file as HEAD has it, and the change bars last computed
// against it.
type gitBase struct {
	data    []byte
	tracked bool
	loaded  bool
	rev     uint64 // buffer revision the marks were computed for
	busy    bool
	marks   map[int]ui.LineMarkKind
}

// startGit wires the git panel, the file watcher and the status poll to the
// open folder.
func (a *App) startGit() {
	a.stopGit()
	root := a.ws.Root
	g := &a.gitst
	g.bases = map[string]*gitBase{}
	g.page = gitui.NewGitPage(gitui.GitDeps{
		Dir:      func() (string, error) { return root, nil },
		OpenFile: a.openFile,
		Dialogs:  func() *ui.DialogHost { return a.ctx.Dialogs() },
		Error:    func(err error) { a.fail("Git", err) },
		Toast:    func(s string) { a.toast(s, ui.ToastInfo) },
		Wake:     a.invalidate,
		HasDirty: a.ws.HasDirty,
		Reload:   a.reloadFromDisk,
		Changed:  a.gitChanged,
	})
	g.page.Refresh()
	g.polled = time.Now()
	stop := make(chan struct{})
	g.stop = stop
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		gx, err := gitx.Find(ctx)
		var repo *gitx.Repo
		if err == nil {
			repo, _ = gx.Open(ctx, root)
		}
		a.onUI(func() {
			if a.ws.Root == root {
				a.gitst.repo = repo
			}
		})
	}()
	go func() {
		t := time.NewTicker(gitPollInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.invalidate()
			}
		}
	}()
	if w, err := workspace.Watch(root, func(name string) bool { return hiddenNames[name] || skipDirs[name] }, a.invalidate); err == nil {
		g.watcher = w
	} else {
		a.notifs.Add("File changes on disk will not show until you refresh: "+err.Error(), ui.ToastWarning)
	}
}

func (a *App) stopGit() {
	g := &a.gitst
	if g.stop != nil {
		close(g.stop)
		g.stop = nil
	}
	g.watcher.Close()
	g.watcher = nil
	if g.page != nil {
		g.page.Close()
	}
	*g = gitState{}
}

// gitChanged takes a fresh status from the git page.
func (a *App) gitChanged(st *gitx.Status) {
	g := &a.gitst
	g.status = st
	if st != nil && st.Head != g.head {
		g.head = st.Head
		g.bases = map[string]*gitBase{} // HEAD moved: every base is stale
	}
}

// pumpGit applies git results, file changes from disk and the periodic
// status poll; call it from Body.
func (a *App) pumpGit(c *ui.Ctx) {
	g := &a.gitst
	if g.page == nil {
		return
	}
	g.page.Poll(c)
	ch := g.watcher.Take()
	if !ch.Empty() {
		a.diskChanged(ch)
	}
	if time.Since(g.polled) >= gitPollInterval || ch.Git || len(ch.Files) > 0 {
		g.polled = time.Now()
		g.page.Refresh()
	}
	if ed := a.ws.ActiveEditor(); ed != nil {
		a.updateChangeBars(ed)
	}
	if p := a.search.preview; a.search.open && p != "" {
		if ed := a.bufs.Get(p); ed != nil {
			a.updateChangeBars(ed)
		}
	}
}

// diskChanged follows edits made outside hm: the tree, the file list, and
// open files that were not edited here.
func (a *App) diskChanged(ch workspace.Changes) {
	for dir := range ch.Dirs {
		a.tree.Reload(dir)
	}
	if len(ch.Dirs) > 0 {
		a.idx.refresh()
	}
	for p := range ch.Files {
		if b := a.gitst.bases[p]; b != nil && !b.tracked {
			delete(a.gitst.bases, p)
		}
		a.reloadIfClean(p)
	}
	if ch.Git {
		a.reloadFromDisk()
	}
}

// reloadFromDisk refreshes every open file after git rewrote files.
func (a *App) reloadFromDisk() {
	for _, p := range a.openBuffers() {
		a.reloadIfClean(p)
	}
}

// reloadIfClean replaces an unedited buffer with the file's new content;
// an edited one is kept, with a notice.
func (a *App) reloadIfClean(path string) {
	ed := a.bufs.Get(path)
	if ed == nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.Equal(data, ed.Bytes()) {
		return
	}
	if ed.Modified() {
		a.notifs.Add(a.ws.Rel(path)+" changed on disk; your unsaved version is kept", ui.ToastWarning)
		return
	}
	ed.ApplyText(string(data))
	ed.MarkSaved()
}

// updateChangeBars recomputes the gutter change bars against HEAD when the
// buffer changed, off the UI goroutine.
func (a *App) updateChangeBars(ed *ui.Editor) {
	g := &a.gitst
	if g.repo == nil || g.status == nil || g.status.Head == "" {
		ed.SetLineMarks(nil)
		return
	}
	path := ed.Path
	b := g.bases[path]
	if b == nil {
		b = &gitBase{}
		g.bases[path] = b
		repo, rel := g.repo, filepath.ToSlash(a.ws.Rel(path))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			data, err := repo.ShowFile(ctx, "HEAD", rel)
			a.onUI(func() {
				if g.bases[path] == b {
					b.data, b.tracked, b.loaded = data, err == nil, true
					b.rev = 0
				}
			})
		}()
		return
	}
	if !b.loaded || b.busy {
		return
	}
	if rev := ed.Revision() + 1; b.rev != rev {
		b.rev, b.busy = rev, true
		base, text, tracked := b.data, append([]byte(nil), ed.Bytes()...), b.tracked
		go func() {
			var marks map[int]ui.LineMarkKind
			if tracked {
				marks = map[int]ui.LineMarkKind{}
				for l, m := range diff.Marks(diff.Lines(string(base)), diff.Lines(string(text))) {
					marks[l] = ui.LineMarkKind(m)
				}
			} else {
				// Untracked: every line is new.
				marks = map[int]ui.LineMarkKind{}
				for l := range diff.Lines(string(text)) {
					marks[l] = ui.LineAdded
				}
			}
			a.onUI(func() {
				b.busy, b.marks = false, marks
				if e := a.bufs.Get(path); e != nil {
					e.SetLineMarks(marks)
				}
			})
		}()
	}
}
