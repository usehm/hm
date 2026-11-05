package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/theme"
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
	bases   map[string]*gitBase        // HEAD versions of open files, for change bars
	kinds   map[string]gitx.ChangeKind // absolute path -> change, files and the folders holding them
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
	if w, err := workspace.Watch(root, func(name string) bool { return hiddenNames[name] || skipDirs[name] }, a.invalidate); err == nil {
		g.watcher = w
	} else {
		a.notifs.Add("File changes on disk will not show until you refresh: "+err.Error(), ui.ToastWarning)
	}
}

func (a *App) stopGit() {
	g := &a.gitst
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
	g.kinds = changeKinds(a.ws.Root, st)
	if st != nil && st.Head != g.head {
		g.head = st.Head
		g.bases = map[string]*gitBase{} // HEAD moved: every base is stale
	}
}

// openGit shows the Git page as a tab.
func (a *App) openGit() {
	if !a.gitTabActive() && a.gitst.page != nil {
		// The work tree changes outside hm; read it each time the page shows.
		a.gitst.page.Refresh()
	}
	a.ws.OpenPage(workspace.KindGit)
}

// toggleGit opens the Git tab, or closes it when it is the active tab.
func (a *App) toggleGit() {
	if a.gitTabActive() {
		a.ws.Close(a.ws.Active)
		return
	}
	a.openGit()
}

func (a *App) gitTabActive() bool {
	t := a.ws.ActiveTab()
	return t != nil && t.Kind == workspace.KindGit
}

// gitPage is the Git tab's content.
func (a *App) gitPage(c *ui.Ctx) ui.View {
	if a.gitst.page == nil {
		return ui.EmptyState("No repository", "This folder is not a git repository").Grow(1)
	}
	return ui.ViewOf(a.gitst.page.Layout(c)).Grow(1)
}

// changeKinds maps each changed file, and every folder above it, to its kind
// of change. A folder takes the most notable kind among its files.
func changeKinds(root string, st *gitx.Status) map[string]gitx.ChangeKind {
	if st == nil || len(st.Changes) == 0 {
		return nil
	}
	out := make(map[string]gitx.ChangeKind, len(st.Changes)*2)
	for _, ch := range st.Changes {
		p := filepath.Join(root, filepath.FromSlash(ch.Path))
		out[p] = ch.Kind
		for d := filepath.Dir(p); len(d) > len(root); d = filepath.Dir(d) {
			if k, ok := out[d]; ok && changeRank(k) >= changeRank(ch.Kind) {
				break
			}
			out[d] = ch.Kind
		}
	}
	return out
}

// changeRank orders kinds by how much a folder should show them.
func changeRank(k gitx.ChangeKind) int {
	switch k {
	case gitx.Conflicted:
		return 3
	case gitx.Modified, gitx.Renamed, gitx.Deleted:
		return 2
	}
	return 1 // added, untracked
}

// treeLabelColor colors explorer names by git status: new files green,
// changed ones yellow, conflicts red.
func (a *App) treeLabelColor(path string, _ bool) (render.Color, bool) {
	k, ok := a.gitst.kinds[path]
	if !ok {
		return render.Color{}, false
	}
	th := theme.Current()
	switch k {
	case gitx.Conflicted:
		return th.ErrorForeground, true
	case gitx.Added, gitx.Untracked:
		return th.SuccessForeground, true
	}
	return th.WarningForeground, true
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
	if wait := gitPollInterval - time.Since(g.polled); wait <= 0 || ch.Git || len(ch.Files) > 0 {
		g.polled = time.Now()
		if a.gitTabActive() {
			g.page.Refresh()
		} else {
			// Hidden page: the footer and change bars need only the status.
			g.page.RefreshStatus()
		}
		c.Animate(gitPollInterval)
	} else {
		c.Animate(wait)
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
	a.contextChanged(ch)
	a.promptsChanged(ch)
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
		a.notifs.Add(a.ws.Rel(path)+" changed on disk; your unsaved version is kept", ui.ToastWarning, a.openFileAction(path))
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
			}
			// A new file gets no bars: the explorer colors its name instead.
			a.onUI(func() {
				b.busy, b.marks = false, marks
				if e := a.bufs.Get(path); e != nil {
					e.SetLineMarks(marks)
				}
			})
		}()
	}
}
