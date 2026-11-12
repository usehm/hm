// Package app is hm's window: the shell around the editor, its commands, and
// the wiring between the packages below it.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/config"
	"github.com/usehm/hm/internal/editor"
	"github.com/usehm/hm/internal/lang"
	"github.com/usehm/hm/internal/secrets"
	"github.com/usehm/hm/internal/vim"
	"github.com/usehm/hm/internal/workspace"
)

// App holds everything that outlives a frame.
type App struct {
	bufs *editor.Buffers
	ws   *workspace.Workspace
	tree *ui.FileTree
	idx  *fileIndex

	notifs      Notifications
	nav         navHistory
	rec         *keyRecorder // a shortcut being recorded in Settings
	kbQuery     string       // the shortcuts page's filter
	keyCommands []keyCommand
	vims        map[*ui.Editor]*vim.State // each file editor's Vim keymap
	groups      []*editorGroup            // split view's groups after the first
	focusGroup  int                       // 0 is the first group, i is groups[i-1]
	notifsOpen  bool
	sideOpen    bool
	panel       panelKind
	panelOpen   bool
	agentOpen   bool
	treeQuery   string
	fileClip    fileClip          // files copied or cut in the tree
	mdDocs      map[string]*mdDoc // preview state per Markdown file
	noSession   bool
	noUpdates   bool

	cfg             *config.Manager
	applied         appliedConfig
	problemsKey     string // last reported config problems, to toast once
	settingsProject bool   // the settings page edits the project file
	settingsCat     string // the settings page's category; "" is the first
	settingsLang    string // the editor settings' language; "" is all languages
	pendingText     *pendingText
	lastEdit        map[string]editSeen
	lastActive      string

	lang        *lang.Service
	uiq         chan func()
	formatting  map[string]int // in-flight formatter runs by path
	installing  map[string]bool
	locations   []*ui.Command // last references/symbols list
	pasted      *pasteMark
	output      strings.Builder
	outputEd    *ui.Editor
	outputDirty bool

	search projectSearch
	gitst  gitState
	term   terminals
	agent  agentState
	compl  complState
	upd    *updater

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
	// KeepPath skips extending PATH from the login shell.
	KeepPath bool
	// NoUpdates skips looking for new releases.
	NoUpdates bool
	// Keys replaces the OS keychain, for tests.
	Keys secrets.Store
}

// Build creates the app. It runs inside yoga.Run's build function, after the
// text engine exists, because opening files builds editors.
func Build(opts Options) *App {
	// "ctrl+`" means Control on macOS, as in other editors, and "cmd+s"
	// means Command.
	ui.LiteralCtrl = true
	a := &App{
		sideOpen: true, noSession: opts.NoSession, lastEdit: map[string]editSeen{},
		uiq: make(chan func(), 256), formatting: map[string]int{}, installing: map[string]bool{},
		upd: &updater{}, noUpdates: opts.NoUpdates,
	}
	a.agent.keys = opts.Keys
	a.bufs = editor.New(editorOptions)
	a.bufs.OnOpen = a.attachEditor
	a.lang = lang.New(a.invalidate)
	a.lang.Manager().OnApplyEdit(a.serverApplyEdit)
	if opts.KeepPath {
		a.lang.MarkPathReady()
	} else {
		a.lang.FixPathAsync()
	}

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
	a.notifs.Close = func() { a.notifsOpen = false }
	for _, p := range a.cfg.Problems() {
		a.notifs.Add("Config: "+p.Error(), ui.ToastError, a.openFileAction(p.File))
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
		a.restoreViews(sess)
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
	first := a.ctx == nil
	if first {
		a.wakeCtx.Store(c)
	}
	a.ctx = c
	if first && !a.noUpdates {
		a.startUpdateCheck(a.invalidate)
	}
	a.reportUpdate()
	a.drainUI()
	a.reportLanguages()
	a.formatAfterPaste()
	a.pumpSearch(c)
	a.pumpGit(c)
	a.pumpCompletion(c)
	a.takeDrops(c)
	if a.cfg.TakeChange() {
		a.reloadConfig()
	}
	a.flushPending(c)
	a.idx.drain()
	a.syncMirrors()
	a.registerCommands(c)
	a.trackNav()
	a.syncCarets()
	if ed := a.curEditor(); ed != nil && !a.modalOpen(c) {
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
func (a *App) OnKey(c *ui.Ctx, k input.KeyEvent) bool {
	if a.recordKeyEvent(k) {
		return true
	}
	if a.pasteImage(c, k) {
		return true
	}
	if k.Key == input.KeyV && k.Mods.Primary() {
		// Let the paste happen, then run "on: paste" formatters.
		if ed := a.ws.ActiveEditor(); ed != nil && ed.Focused() {
			a.pasted = &pasteMark{path: ed.Path, rev: ed.Revision(), at: time.Now()}
		}
	}
	return false
}

// attachEditor wires a new file editor to the app.
func (a *App) attachEditor(ed *ui.Editor) {
	a.attachVim(ed)
	a.applyEditorSettings(ed, ed.Path)
	ed.OnModClick = func(off int) bool {
		if !a.lang.HasServer(ed.Path) {
			return false
		}
		a.definition(ed, off)
		return true
	}
	ed.ContextMenu = func(items []ui.MenuItem) []ui.MenuItem {
		if !a.lang.HasServer(ed.Path) {
			return append(items, ui.MenuItem{Label: "Format Document", OnSelect: func() { a.format(ed, "manual", nil) }})
		}
		return append([]ui.MenuItem{
			{Label: "Ask Agent About Selection", Disabled: !ed.HasSelection(), OnSelect: a.askAboutSelection},
			{Label: "Go to Definition", OnSelect: func() { a.definition(ed, ed.Caret()) }},
			{Label: "Go to Implementation", OnSelect: func() { a.implementation(ed, ed.Caret()) }},
			{Label: "Find References", OnSelect: func() { a.references(ed, ed.Caret()) }},
			{Label: "Rename Symbol…", OnSelect: func() { a.rename(ed) }},
			{Label: "Format Document", OnSelect: func() { a.format(ed, "manual", nil) }},
		}, items...)
	}
}

// Close saves the session and releases editors and watchers.
func (a *App) Close() {
	a.saveSession()
	a.stopAllAgents()
	a.closeAllTerminals()
	a.agent.materialize.Wait()
	for _, r := range a.runs() {
		r.shells.StopAll()
	}
	if a.agent.mcp != nil {
		a.agent.mcp.Close()
	}
	if a.search.cancel != nil {
		a.search.cancel()
	}
	a.cfg.Close()
	a.stopGit()
	a.idx.stop()
	a.bufs.CloseAll()
}

// restoreViews brings back the agent, its session and the bottom panel as
// the last run left them.
func (a *App) restoreViews(s workspace.Session) {
	a.agentOpen = s.Agent
	tabs := s.AgentTabs
	if len(tabs) == 0 && s.AgentSession != "" {
		tabs = []workspace.AgentTab{{Kind: agentTabSession, Key: s.AgentSession}}
	}
	a.restoreAgentTabs(tabs, s.AgentActive)
	for i, p := range panelTabs {
		if p.Value == s.Panel {
			a.showPanel(panelKind(i))
		}
	}
	for _, paths := range s.Groups {
		if len(a.groups)+1 >= maxGroups {
			break
		}
		g := &editorGroup{mirrors: map[string]*mirror{}}
		for _, p := range paths {
			if fileExists(p) {
				a.groupOpen(g, p)
			}
		}
		if len(g.paths) > 0 {
			a.groups = append(a.groups, g)
		}
	}
	a.focusGroup = 0
}

func (a *App) saveSession() {
	if a.ws == nil || a.noSession {
		return
	}
	s := a.ws.Snapshot()
	side := a.sideOpen
	s.Side = &side
	s.Agent = a.agentOpen
	s.AgentTabs, s.AgentActive = a.agentTabsState()
	if a.panelOpen {
		s.Panel = panelTabs[a.panel].Value
	}
	for _, g := range a.groups {
		s.Groups = append(s.Groups, append([]string(nil), g.paths...))
	}
	_ = workspace.SaveSession(s)
}

// toast shows a transient message and keeps it in the notification center.
func (a *App) toast(msg string, kind ui.ToastVariant, actions ...notifAction) {
	a.notifs.Add(msg, kind, actions...)
	if a.ctx != nil {
		a.ctx.Toasts().Show(msg, kind, 3*time.Second)
	}
}

// openFileAction is a notification action that opens path; none for
// paths that are not files, like "defaults".
func (a *App) openFileAction(path string) notifAction {
	if !filepath.IsAbs(path) {
		return notifAction{}
	}
	return notifAction{label: "Open File", run: func() { a.openFile(path) }}
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
