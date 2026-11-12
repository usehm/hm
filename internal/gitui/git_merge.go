// Adapted from chapar ui/pages/git_merge.go (github.com/chapar-rest/chapar@2342e52a).

package gitui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/gitx"
)

// --- branches -----------------------------------------------------------------

// branchPicker switches branches. Remote branches with no local branch
// are listed too; picking one makes a local branch that tracks it.
func (g *Git) branchPicker(c *ui.Ctx, s *gitSnapshot, current string) ui.View {
	var opts []ui.SelectOption
	sel := -1
	for i, b := range s.branches {
		label := b.Name
		if b.Remote {
			label += " (remote)"
		}
		opts = append(opts, ui.SelectOption{Label: label, Value: b.Name})
		if b.Current {
			sel = i
		}
	}
	if sel < 0 {
		// Detached HEAD, or a branch with no commits yet.
		opts = append([]ui.SelectOption{{Label: current, Value: ""}}, opts...)
		sel = 0
	}
	return ui.Select("git-branch", opts).Width(220).Selected(sel).
		Disabled(g.busy != "" || s.op != gitx.OpNone).
		OnChange(func(name string) {
			for _, b := range s.branches {
				if b.Name == name && !b.Current {
					g.switchBranch(b)
				}
			}
		})
}

// branchMenu holds the branch actions besides switching.
func (g *Git) branchMenu(s *gitSnapshot) ui.View {
	idle := g.busy == "" && s.op == gitx.OpNone
	cur := s.status.Branch
	items := []ui.MenuItem{
		{Label: "New branch…", Disabled: !idle || s.status.Head == "", OnSelect: g.askNewBranch},
	}
	if cur != "" {
		items = append(items, ui.MenuItem{Label: "Merge a branch into " + cur + "…",
			Disabled: !idle || len(s.branches) < 2, OnSelect: g.askMerge})
	}
	return ui.IconButton("git-branch-menu", icons.GitBranchPlus).Tooltip("Branch actions").Menu(items)
}

func (g *Git) switchBranch(b gitx.Branch) {
	if g.blockedByDirty("Switching branches") {
		return
	}
	repo := g.snap.repo
	g.action("Switching…", func(ctx context.Context) func() {
		err := repo.Switch(ctx, b)
		return func() {
			if err != nil {
				g.fail(err)
				return
			}
			g.reloadWorkspace()
			g.deps.Toast("Switched to " + strings.TrimPrefix(b.Name, remotePrefix(b)))
			g.Refresh()
		}
	})
}

// remotePrefix is "origin/" for a remote branch, else "".
func remotePrefix(b gitx.Branch) string {
	if !b.Remote {
		return ""
	}
	if i := strings.Index(b.Name, "/"); i >= 0 {
		return b.Name[:i+1]
	}
	return ""
}

func (g *Git) askNewBranch() {
	host := g.dialogs()
	if host == nil {
		return
	}
	repo := g.snap.repo
	host.ShowInput("New branch", "Branch name, such as fix/auth-headers", func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		g.action("Creating branch…", func(ctx context.Context) func() {
			err := repo.CreateBranch(ctx, name)
			return func() {
				if err != nil {
					g.fail(err)
					return
				}
				g.deps.Toast("Created and switched to " + name)
				g.Refresh()
			}
		})
	}, nil)
}

// askMerge picks a branch to merge into the current one.
func (g *Git) askMerge() {
	host := g.dialogs()
	if host == nil || g.blockedByDirty("Merging") {
		return
	}
	var opts []ui.SelectOption
	for _, b := range g.snap.branches {
		if !b.Current {
			opts = append(opts, ui.SelectOption{Label: b.Name, Value: b.Name})
		}
	}
	if len(opts) == 0 {
		return
	}
	pick := opts[0].Value
	cur := g.snap.status.Branch
	host.Show(ui.DialogOpts{
		Title:  "Merge into " + cur,
		Width:  440,
		Height: 260,
		Body: func(c *ui.Ctx) ui.View {
			th := c.Theme()
			sel := 0
			for i, o := range opts {
				if o.Value == pick {
					sel = i
				}
			}
			return ui.Column(
				ui.Paragraph("Bring another branch's commits into "+cur+". Files both branches changed "+
					"in the same place are listed for you to resolve."),
				ui.Select("git-merge-pick", opts).Selected(sel).OnChange(func(v string) { pick = v }),
			).Gap(th.Spacing.M).Padding(th.Spacing.L)
		},
		Actions: []ui.DialogAction{
			{Label: "Cancel"},
			{Label: "Merge", Primary: true, OnClick: func() { g.merge(pick) }},
		},
	})
}

// merge merges ref into the current branch. Conflicts leave the merge in
// progress, with the Changes tab listing them.
func (g *Git) merge(ref string) {
	if g.blockedByDirty("Merging") {
		return
	}
	repo := g.snap.repo
	g.action("Merging…", func(ctx context.Context) func() {
		conflicts, err := repo.Merge(ctx, ref)
		return func() { g.afterStop(err, conflicts, "Merged "+ref) }
	})
}

// afterStop reports a merge or revert: done, stopped with conflicts, or
// failed.
func (g *Git) afterStop(err error, conflicts bool, done string) {
	if err != nil {
		g.fail(err)
		return
	}
	g.reloadWorkspace()
	if conflicts {
		g.tab = 0
		g.deps.Toast("Some files changed on both sides. Resolve each one, then finish.")
	} else {
		g.deps.Toast(done)
	}
	g.Refresh()
}

// offerMerge follows a pull that couldn't fast-forward.
func (g *Git) offerMerge() {
	host := g.dialogs()
	up := g.snap.status.Upstream
	if host == nil || up == "" {
		g.fail(gitx.ErrDiverged)
		return
	}
	host.ShowAction("Merge the remote's changes?",
		"This branch and "+up+" both have new commits. hm can merge them; any file both sides changed "+
			"in the same place is listed for you to resolve.",
		func() { g.merge(up) }, nil)
}

// --- in progress ---------------------------------------------------------

func (g *Git) conflicts() int {
	n := 0
	for _, e := range g.changes.entries {
		if e.change.Kind == gitx.Conflicted {
			n++
		}
	}
	return n
}

// opBanner replaces the commit box while git is in the middle of a merge
// or revert.
func (g *Git) opBanner(c *ui.Ctx) ui.View {
	th := c.Theme()
	s := g.snap
	if s.op != gitx.OpMerge && s.op != gitx.OpRevert {
		return ui.Row(
			ui.Icon(icons.TriangleAlert, th.Metrics.IconSizeSM, th.Warning),
			ui.Paragraph("A "+s.op.String()+" is in progress. Finish or abort it in a terminal, then refresh.").Grow(1),
		).Gap(th.Spacing.S).Align(ui.AlignCenter).PaddingXY(th.Spacing.M, th.Spacing.S)
	}
	title := s.opMsg
	if title == "" {
		title = "A " + s.op.String() + " is in progress"
	}
	n := g.conflicts()
	status := "Every conflict is resolved"
	switch {
	case n == 1:
		status = "1 file has conflicts"
	case n > 1:
		status = fmt.Sprintf("%d files have conflicts", n)
	}
	finish := "Finish merge"
	if s.op == gitx.OpRevert {
		finish = "Finish revert"
	}
	idle := g.busy == ""
	return ui.Row(
		ui.Icon(icons.GitMerge, th.Metrics.IconSizeSM, th.Accent),
		ui.Column(
			ui.Strong(title),
			ui.Caption(status),
		).Gap(th.Spacing.XXS).Grow(1),
		ui.Button("git-abort", ui.Text("Abort")).Disabled(!idle).OnClick(g.confirmAbort),
		ui.Button("git-finish", ui.Text(finish)).Primary().Disabled(!idle || n > 0).OnClick(g.finish),
	).Gap(th.Spacing.S).Align(ui.AlignCenter).PaddingXY(th.Spacing.M, th.Spacing.S)
}

func (g *Git) confirmAbort() {
	host := g.dialogs()
	if host == nil || g.blockedByDirty("Aborting") {
		return
	}
	repo, op := g.snap.repo, g.snap.op
	host.ShowAction("Abort the "+op.String()+"?",
		"The branch and its files go back to how they were before it started, and the versions you picked are lost.",
		func() {
			g.action("Aborting…", func(ctx context.Context) func() {
				err := repo.Abort(ctx, op)
				return func() {
					if err != nil {
						g.fail(err)
						return
					}
					g.reloadWorkspace()
					g.Refresh()
				}
			})
		}, nil)
}

func (g *Git) finish() {
	repo, op := g.snap.repo, g.snap.op
	g.action("Committing…", func(ctx context.Context) func() {
		err := repo.Continue(ctx, op)
		return func() {
			if err != nil {
				g.fail(err)
				return
			}
			g.deps.Toast("Finished the " + op.String())
			g.Refresh()
		}
	})
}

// --- resolving a conflicted file -------------------------------------------

// gitResolve is a conflicted file waiting to be settled.
type gitResolve struct {
	path     string
	key      string
	loading  bool
	err      error
	versions gitx.Versions
}

func (g *Git) loadResolve(path string) {
	key := fmt.Sprintf("%d:%s", g.refreshSeq, path)
	if g.resolve.key == key {
		return
	}
	g.resolve = gitResolve{path: path, key: key, loading: true}
	repo := g.snap.repo
	g.do(func(ctx context.Context) func() {
		v, err := repo.ConflictVersions(ctx, path)
		return func() {
			if g.resolve.key != key {
				return
			}
			g.resolve.loading, g.resolve.err, g.resolve.versions = false, err, v
		}
	})
}

// sideNames name the two sides for the current operation.
func (g *Git) sideNames() (ours, theirs string) {
	ours = "This branch"
	if b := g.snap.status.Branch; b != "" {
		ours = b
	}
	if g.snap.op == gitx.OpRevert {
		return ours, "The revert"
	}
	return ours, "Incoming"
}

// resolvePane settles a conflicted file: edit the conflict markers git
// wrote into it and mark it resolved, or take one side's version whole.
func (g *Git) resolvePane(c *ui.Ctx, e *gitEntry) ui.View {
	th := c.Theme()
	r := &g.resolve
	ent := *e
	head := ui.Column(
		ui.Strong(e.item.label()),
		ui.Caption("Conflicted"),
	).Gap(th.Spacing.XXS)
	switch {
	case r.path != e.change.Path || r.loading:
		return paneCard(th, head, mutedParagraph("Reading both versions…"))
	case r.err != nil:
		return paneCard(th, head, ui.Paragraph(r.err.Error()).Style(ui.Spec{}.TextColor(ui.TokenError)))
	}
	ours, theirs := g.sideNames()
	keep := func(name string, data []byte) string {
		if data == nil {
			return "Delete it, as " + name + " did"
		}
		return "Keep " + name + "'s version"
	}
	idle := g.busy == ""
	return paneCard(th,
		head,
		ui.Paragraph("Both sides changed this file. Git marked each clash with <<<<<<<, ======= and >>>>>>> lines. "+
			"Open the file, keep the code you want, remove the markers and save, then mark it resolved."),
		ui.Row(
			ui.Button("git-res-open", ui.Text("Open file")).Primary().IconStart(icons.SquareArrowOutUpRight).
				OnClick(func() { g.openItem(&ent) }),
			ui.Button("git-res-mark", ui.Text("Mark resolved")).IconStart(icons.Check).
				Disabled(!idle).Tooltip("Stage the file as it is on disk").
				OnClick(func() { g.markResolved(ent) }),
		).Gap(th.Spacing.S),
		mutedParagraph("Or take one side's version of the whole file:"),
		ui.Row(
			ui.Button("git-res-whole-o", ui.Text(keep(ours, r.versions.Ours))).
				Disabled(!idle).OnClick(func() { g.resolveWith(r.path, r.versions.Ours) }),
			ui.Button("git-res-whole-t", ui.Text(keep(strings.ToLower(theirs), r.versions.Theirs))).
				Disabled(!idle).OnClick(func() { g.resolveWith(r.path, r.versions.Theirs) }),
		).Gap(th.Spacing.S),
		ui.Spacer(),
	)
}

// markResolved stages the file as it is on disk, refusing while conflict
// markers are left in it.
func (g *Git) markResolved(e gitEntry) {
	if g.blockedByDirty("Resolving") {
		return
	}
	path := filepath.Join(g.snap.dir, filepath.FromSlash(e.change.Path))
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		g.deps.Error(err)
		return
	}
	if hasConflictMarkers(data) {
		g.deps.Error(gitMessage(e.item.label() + " still has conflict markers. Remove them and save, then mark it resolved."))
		return
	}
	if os.IsNotExist(err) {
		data = nil
	}
	g.resolveWith(e.change.Path, data)
}

// hasConflictMarkers reports whether data holds a line git writes around a
// conflict.
func hasConflictMarkers(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "<<<<<<< ") || strings.HasPrefix(line, ">>>>>>> ") || line == "=======" {
			return true
		}
	}
	return false
}

// resolveWith writes the resolved file, or deletes it for nil data, and
// marks it resolved.
func (g *Git) resolveWith(path string, data []byte) {
	if g.blockedByDirty("Resolving") {
		return
	}
	repo := g.snap.repo
	g.action("Resolving…", func(ctx context.Context) func() {
		err := repo.Resolve(ctx, path, data)
		return func() {
			if err != nil {
				g.fail(err)
				return
			}
			g.reloadWorkspace()
			g.Refresh()
		}
	})
}

// --- revert ----------------------------------------------------------------

// commitHeader names the selected commit above its files, with Revert.
func (g *Git) commitHeader(c *ui.Ctx) ui.View {
	th := c.Theme()
	cm := g.commitByHash(g.selCommit)
	if cm == nil {
		return nil
	}
	commit := *cm
	idle := g.busy == "" && g.snap.op == gitx.OpNone
	return ui.Row(
		ui.Column(
			ui.Strong(commit.Subject),
			ui.Caption(commit.Short+" · "+commit.Author+" · "+commit.When.Format("2006-01-02 15:04")),
		).Gap(th.Spacing.XXS).Grow(1),
		ui.Button("git-revert", ui.Text("Revert")).IconStart(icons.Undo2).Ghost().HoverFill().
			Disabled(!idle).Tooltip("Make a new commit that undoes this one").
			OnClick(func() { g.confirmRevert(commit) }),
	).Gap(th.Spacing.S).Align(ui.AlignCenter).PaddingXY(th.Spacing.S, th.Spacing.XS)
}

func (g *Git) confirmRevert(cm gitx.Commit) {
	host := g.dialogs()
	if host == nil || g.blockedByDirty("Reverting") {
		return
	}
	host.ShowAction("Revert “"+cm.Subject+"”?",
		"hm makes a new commit that undoes its changes. The commit stays in the history.",
		func() { g.revert(cm) }, nil)
}

func (g *Git) revert(cm gitx.Commit) {
	repo := g.snap.repo
	g.action("Reverting…", func(ctx context.Context) func() {
		conflicts, err := repo.Revert(ctx, cm)
		return func() { g.afterStop(err, conflicts, "Reverted "+cm.Short) }
	})
}
