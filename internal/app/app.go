// Package app is hm's window: the shell around the editor, its commands, and
// the wiring between the packages below it.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/editor"
	"github.com/mirzakhany/hm/internal/workspace"
)

// App holds everything that outlives a frame.
type App struct {
	bufs *editor.Buffers
	ws   *workspace.Workspace
	tree *ui.FileTree
	idx  *fileIndex

	notifs     Notifications
	notifsOpen bool
	sideOpen   bool
	panel      panelKind
	panelOpen  bool
	agentOpen  bool
	treeQuery  string
	noSession  bool

	cfg             *config.Manager
	applied         appliedConfig
	problemsKey     string // last reported config problems, to toast once
	settingsProject bool   // the settings page edits the project file
	pendingText     *pendingText
	lastEdit        map[string]editSeen
	lastActive      string

	// ctx is the window context, valid for the window's lifetime. Callbacks
	// that run outside Body use it for dialogs, toasts and wakes.
	ctx *ui.Ctx
	// wakeCtx is ctx for goroutines, which only call Invalidate.
	wakeCtx atomic.Pointer[ui.Ctx]
}

var (
	_ yoga.App     = (*App)(nil)
	_ yoga.Closer  = (*App)(nil)
	_ yoga.KeyHook = (*App)(nil)
)

// Options configure Build; tests set them, main leaves them zero.
type Options struct {
	// Root is the folder to open; empty restores the last session's folder.
	Root string
	// NoSession skips restoring and saving the session.
	NoSession bool
}

// Build creates the app. It runs inside yoga.Run's build function, after the
// text engine exists, because opening files builds editors.
func Build(opts Options) *App {
	a := &App{sideOpen: true, noSession: opts.NoSession, lastEdit: map[string]editSeen{}}
	a.bufs = editor.New(editorOptions)

	var sess workspace.Session
	if !opts.NoSession {
		s, err := workspace.LoadSession()
		if err != nil {
			a.notifs.Add("Could not read the last session: "+err.Error(), ui.ToastWarning)
		}
		sess = s
		if sess.Side != nil {
			a.sideOpen = *sess.Side
		}
	}
	root := opts.Root
	if root == "" {
		root = sess.Root
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	a.cfg = config.NewManager(config.UserPath(), config.ProjectPath(root))
	if err := config.WriteSchema(); err != nil {
		a.notifs.Add("Could not write the config schema: "+err.Error(), ui.ToastWarning)
	}
	for _, p := range a.cfg.Problems() {
		a.notifs.Add("Config: "+p.Error(), ui.ToastError)
	}
	a.problemsKey = fmt.Sprint(a.cfg.Problems())
	a.openFolder(root)
	if err := a.cfg.Watch(a.invalidate); err != nil {
		a.notifs.Add("Config changes will not apply live: "+err.Error(), ui.ToastWarning)
	}
	if !opts.NoSession && sess.Root == a.ws.Root {
		for _, p := range a.ws.Restore(sess) {
			a.notifs.Add("Could not reopen "+p, ui.ToastWarning)
		}
	}
	return a
}

// editorOptions are the options every file editor is built with.
func editorOptions() []ui.EditorOption {
	return []ui.EditorOption{
		ui.WithWrapIndent(ui.WrapHanging),
		ui.WithGutter(ui.GutterSpec{MinDigits: 3}),
	}
}

func (a *App) Body(c *ui.Ctx) ui.View {
	if a.ctx == nil {
		a.wakeCtx.Store(c)
	}
	a.ctx = c
	if a.cfg.TakeChange() {
		a.reloadConfig()
	}
	a.flushPending(c)
	a.idx.drain()
	a.registerCommands(c)
	if ed := a.ws.ActiveEditor(); ed != nil && !a.modalOpen(c) {
		c.Focus().EnsureFocus(ed)
	}

	return ui.Column(
		a.titleBar(c),
		ui.Row(
			a.center(c),
		).Align(ui.AlignStretch).Grow(1),
		a.footer(c),
	).Grow(1).Background(ui.TokenSurface)
}

func (a *App) modalOpen(c *ui.Ctx) bool {
	return c.Dialogs().Open || c.Commands().Open
}

// OnKey handles global chords a focused editor would otherwise eat. Command
// shortcuts already ran by the time it is called.
func (a *App) OnKey(_ *ui.Ctx, _ input.KeyEvent) bool { return false }

// Close saves the session and releases editors and watchers.
func (a *App) Close() {
	a.saveSession()
	a.cfg.Close()
	a.idx.stop()
	a.bufs.CloseAll()
}

func (a *App) saveSession() {
	if a.ws == nil || a.noSession {
		return
	}
	s := a.ws.Snapshot()
	side := a.sideOpen
	s.Side = &side
	_ = workspace.SaveSession(s)
}

// toast shows a transient message and keeps it in the notification center.
func (a *App) toast(msg string, kind ui.ToastVariant) {
	a.notifs.Add(msg, kind)
	if a.ctx != nil {
		a.ctx.Toasts().Show(msg, kind, 3*time.Second)
	}
}

// fail reports err as an error notification.
func (a *App) fail(prefix string, err error) {
	if err != nil {
		a.toast(prefix+": "+err.Error(), ui.ToastError)
	}
}

func (a *App) invalidate() {
	if c := a.wakeCtx.Load(); c != nil {
		c.Invalidate()
	}
}
