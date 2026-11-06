package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/export"
	"github.com/mirzakhany/hm/internal/config"
	"github.com/mirzakhany/hm/internal/gitx"
	"github.com/mirzakhany/hm/internal/secrets"
)

// Export and share: /export writes a session as one file, and /share puts
// its HTML page in a secret GitHub gist. Both redact secrets first.

// gistKey is the keychain entry of the GitHub token /share was given.
const gistKey = "github-gist"

// shareHooks stand in for the browser and GitHub in tests.
type shareHooks struct {
	open     func(path string) error // nil opens with the OS
	api      string                  // "" is export.GitHubAPI
	gitToken func() string           // nil reads git's saved GitHub login
	// confirm and askToken replace the dialogs when set.
	confirm  func(title, msg string, yes func())
	askToken func(ok func(token string))
}

// exportArgs is what /export was asked for.
type exportArgs struct {
	format export.Format
	all    bool
	path   string
}

// parseExportArgs reads "[--md|--jsonl|--html|--format f] [--all] [path]".
// Without a format flag, the path's extension picks one.
func parseExportArgs(s string) (exportArgs, error) {
	var out exportArgs
	var rest []string
	f := strings.Fields(s)
	for i := 0; i < len(f); i++ {
		w := f[i]
		switch {
		case w == "--all":
			out.all = true
		case w == "--html" || w == "--md" || w == "--markdown" || w == "--jsonl":
			out.format, _ = export.ParseFormat(w[2:])
		case w == "--format" || strings.HasPrefix(w, "--format="):
			v, ok := strings.CutPrefix(w, "--format=")
			if !ok {
				if i+1 >= len(f) {
					return out, errors.New("--format needs html, md or jsonl")
				}
				i++
				v = f[i]
			}
			fm, ok := export.ParseFormat(v)
			if !ok {
				return out, fmt.Errorf("Unknown format %s; use html, md or jsonl", v)
			}
			out.format = fm
		case strings.HasPrefix(w, "--"):
			return out, fmt.Errorf("Unknown option %s. /export [--md|--jsonl] [--all] [path]", w)
		default:
			rest = append(rest, w)
		}
	}
	out.path = strings.Join(rest, " ")
	if out.format == "" {
		out.format = export.HTML
		if fm, ok := export.ParseFormat(filepath.Ext(out.path)); ok && filepath.Ext(out.path) != "" {
			out.format = fm
		}
	}
	return out, nil
}

// exportPath is where an export goes: p as typed, relative to root, with
// the default file name when p is a folder; ~/Downloads (or home) when p
// is empty.
func exportPath(p string, f export.Format, title string, now time.Time, root, home string) string {
	name := export.FileName(title, now, f)
	if p == "" {
		dir := filepath.Join(home, "Downloads")
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			dir = home
		}
		return filepath.Join(dir, name)
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if fi, err := os.Stat(p); (err == nil && fi.IsDir()) || strings.HasSuffix(p, string(filepath.Separator)) {
		return filepath.Join(p, name)
	}
	if filepath.Ext(p) == "" {
		p += "." + string(f)
	}
	return p
}

// exportCommand runs /export in r.
func (a *App) exportCommand(r *agentRun, args string) {
	ea, err := parseExportArgs(args)
	if err != nil {
		r.errText = err.Error()
		return
	}
	if len(r.session.Snapshot()) == 0 {
		r.errText = "Nothing to export yet"
		return
	}
	r.errText = ""
	a.exportSession(r.session, ea)
}

// shareCommand runs /share [--all] in r.
func (a *App) shareCommand(r *agentRun, args []string) {
	all := false
	for _, w := range args {
		if w != "--all" {
			r.errText = "Unknown option " + w + ". /share [--all]"
			return
		}
		all = true
	}
	if len(r.session.Snapshot()) == 0 {
		r.errText = "Nothing to share yet"
		return
	}
	r.errText = ""
	a.shareSession(r.session, all)
}

// exportSession writes s as ea says, off the UI goroutine since reading
// the keychain runs a process, then opens the file.
func (a *App) exportSession(s *agent.Session, ea exportArgs) {
	home, _ := os.UserHomeDir()
	root := a.ws.Root
	opts := export.Options{Format: ea.format, AllBranches: ea.all, Palette: exportPalette()}
	known := a.secretSource()
	go func() {
		opts.Redactor = export.NewRedactor(known()...)
		// The title names the file, so it loses its secrets too.
		title, _, _ := s.Meta()
		title, _ = opts.Redactor.Redact(title)
		path := exportPath(ea.path, ea.format, title, time.Now(), root, home)
		res, err := export.Render(s, opts)
		if err == nil {
			err = os.MkdirAll(filepath.Dir(path), 0o755)
		}
		if err == nil {
			err = os.WriteFile(path, res.Data, 0o600)
		}
		a.onUI(func() { a.exported(path, ea.format, res.Redacted, err) })
	}()
}

func (a *App) exported(path string, f export.Format, red []export.Redaction, err error) {
	if err != nil {
		a.fail("Could not export the session", err)
		return
	}
	if f == export.HTML {
		open := a.agent.share.open
		if open == nil {
			open = openURL
		}
		if err := open(path); err != nil {
			a.fail("Could not open the export", err)
		}
	} else {
		a.openFile(path)
	}
	msg, kind := "Exported to "+tildePath(path), ui.ToastSuccess
	if d := export.Describe(red); d != "" {
		msg, kind = msg+". Redacted "+d, ui.ToastWarning
	}
	a.toast(msg, kind, notifAction{label: revealLabel(), run: func() { _ = reveal(path) }})
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
			return "~" + string(filepath.Separator) + rest
		}
	}
	return p
}

// shareJob is an HTML page waiting to be uploaded.
type shareJob struct {
	file, title string
	data        []byte
}

// shareSession renders s's HTML page, says what will be uploaded and
// what was redacted, and uploads it once the user agrees.
func (a *App) shareSession(s *agent.Session, all bool) {
	opts := export.Options{Format: export.HTML, AllBranches: all, Palette: exportPalette()}
	known := a.secretSource()
	go func() {
		opts.Redactor = export.NewRedactor(known()...)
		title, _, _ := s.Meta()
		title, _ = opts.Redactor.Redact(title) // it names the gist's file and description
		res, err := export.Render(s, opts)
		a.onUI(func() {
			if err != nil {
				a.fail("Could not export the session", err)
				return
			}
			j := shareJob{file: export.FileName(title, time.Now(), export.HTML), title: title, data: res.Data}
			msg := "This uploads the conversation, with its tool calls and their output, to GitHub as a secret gist. " +
				"Anyone with the link can read it."
			if d := export.Describe(res.Redacted); d != "" {
				msg += "\n\nRedacted " + d + "."
			} else {
				msg += "\n\nNothing that looks like a key or token was found."
			}
			if c := a.agent.share.confirm; c != nil {
				c("Share this session?", msg, func() { a.uploadShare(j) })
				return
			}
			a.ctx.Dialogs().ShowAction("Share this session?", msg, func() { a.uploadShare(j) }, nil)
		})
	}()
}

// uploadShare creates the gist, asking for a token when there is none or
// it may not create gists.
func (a *App) uploadShare(j shareJob) {
	keys, hooks := a.agent.keys, a.agent.share
	api := hooks.api
	if api == "" {
		api = export.GitHubAPI
	}
	go func() {
		tok := githubToken(keys, hooks.gitToken)
		if tok == "" {
			a.onUI(func() { a.askGistToken(j, "No GitHub token saved") })
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		g, err := export.CreateGist(ctx, nil, api, tok, j.file, "hm session: "+j.title, j.data)
		a.onUI(func() { a.shared(j, g, err) })
	}()
}

func (a *App) shared(j shareJob, g export.Gist, err error) {
	switch {
	case errors.Is(err, export.ErrGistScope) || errors.Is(err, export.ErrBadToken):
		a.askGistToken(j, err.Error())
	case err != nil:
		a.fail("Could not share the session", err)
	default:
		u := export.ViewerURL(a.cfg.Current().Agent.Share.ViewerURL, g)
		a.copyText(u)
		a.toast("Shared as a secret gist; the link is on the clipboard", ui.ToastSuccess,
			notifAction{label: "Open Link", run: func() { _ = openURL(u) }})
	}
}

// askGistToken asks for a GitHub token that may create gists, keeps it in
// the keychain and tries the upload again.
func (a *App) askGistToken(j shareJob, why string) {
	a.toast(why+". Paste a GitHub token with the gist scope to share.", ui.ToastWarning)
	save := func(v string) {
		if err := a.agent.keys.Set(gistKey, strings.TrimSpace(v)); err != nil {
			a.fail("Could not save the token", err)
			return
		}
		a.uploadShare(j)
	}
	if ask := a.agent.share.askToken; ask != nil {
		ask(save)
		return
	}
	a.ctx.Dialogs().ShowInputOpts(ui.InputOpts{
		Title:       "GitHub token with the gist scope",
		Placeholder: "ghp_… or github_pat_…",
		Validate: func(v string) string {
			if strings.TrimSpace(v) == "" {
				return "Paste a token from github.com/settings/tokens"
			}
			return ""
		},
		OnOK: save,
	})
}

// githubToken is the token /share was given, or else the one git uses
// for github.com.
func githubToken(keys secrets.Store, gitToken func() string) string {
	if t, err := keys.Get(gistKey); err == nil && t != "" {
		return t
	}
	if gitToken == nil {
		gitToken = func() string { return gitx.LoadAuth("https://github.com").Password }
	}
	return gitToken()
}

// secretSource returns a function, to call off the UI goroutine, that
// reads every secret hm knows of: provider and web search keys, MCP
// servers' environment and headers, the GitHub tokens, and environment
// variables named like keys.
func (a *App) secretSource() func() []string {
	cfg := a.cfg.Current()
	keys, gitTok := a.agent.keys, a.agent.share.gitToken
	return func() []string {
		var out []string
		add := func(v string) {
			if r, err := config.Resolve(v); err == nil {
				v = r
			}
			out = append(out, v)
			if f := strings.Fields(v); len(f) == 2 { // "Bearer <token>"
				out = append(out, f[1])
			}
		}
		for _, p := range cfg.Agent.Providers {
			if p.APIKey != "" {
				add(p.APIKey)
			}
		}
		if k := cfg.Tools.Web.Search.APIKey; k != "" {
			add(k)
		}
		for _, m := range cfg.Tools.MCP {
			for _, v := range m.Env {
				add(v)
			}
			for _, v := range m.Headers {
				add(v)
			}
		}
		if t, err := keys.Get(gistKey); err == nil {
			out = append(out, t)
		}
		if gitTok == nil {
			gitTok = func() string { return gitx.LoadAuth("https://github.com").Password }
		}
		out = append(out, gitTok())
		for _, kv := range os.Environ() {
			name, val, _ := strings.Cut(kv, "=")
			if secretName(name) {
				out = append(out, val)
			}
		}
		return out
	}
}

// secretName reports whether an environment variable's name says it
// holds a secret.
func secretName(name string) bool {
	n := strings.ToUpper(name)
	for _, s := range []string{"_KEY", "_TOKEN", "_SECRET", "_PASSWORD", "_PAT"} {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	return strings.Contains(n, "API_KEY") || strings.Contains(n, "SECRET")
}

// exportPalette is the page's colours from the current theme's light and
// dark halves; a theme with one half uses it for both.
func exportPalette() export.Palette {
	cur := *theme.Current()
	light, dark := cur, cur
	if t, ok := theme.Get(cur.LightSibling); ok {
		light = t
	}
	if t, ok := theme.Get(cur.DarkSibling); ok {
		dark = t
	}
	return export.Palette{Light: themeColors(light), Dark: themeColors(dark)}
}

func themeColors(t theme.Theme) export.Colors {
	c := func(col render.Color) string {
		if col.A < 1 {
			col = theme.Flatten(col, t.Surface)
		}
		return fmt.Sprintf("#%02x%02x%02x", uint8(col.R*255+0.5), uint8(col.G*255+0.5), uint8(col.B*255+0.5))
	}
	return export.Colors{Background: c(t.Surface), Surface: c(t.Chrome), Text: c(t.Foreground), Muted: c(t.ForegroundMuted),
		Border: c(t.Border), Accent: c(t.Link), Added: c(t.SuccessForeground), Removed: c(t.ErrorForeground),
		Error: c(t.ErrorForeground)}
}

// sessionByID is the session of an open tab or a running agent, or else
// the saved one.
func (a *App) sessionByID(id string) (*agent.Session, error) {
	if i := a.agentTabIndex(agentTabSession, id); i >= 0 {
		if r := a.agent.tabs[i].run; r != nil {
			return r.session, nil
		}
	}
	if r := a.agent.live[id]; r != nil {
		return r.session, nil
	}
	return a.agent.store.Load(id)
}

// sessionActions are the Export and Share items of a session's menus.
func (a *App) sessionActions(id string) []ui.MenuItem {
	with := func(do func(*agent.Session)) func() {
		return func() {
			s, err := a.sessionByID(id)
			if err != nil {
				a.fail("Could not open the session", err)
				return
			}
			do(s)
		}
	}
	return []ui.MenuItem{
		{Label: "Export as HTML", Icon: icons.Download, OnSelect: with(func(s *agent.Session) {
			a.exportSession(s, exportArgs{format: export.HTML})
		})},
		{Label: "Share as Gist…", Icon: icons.Share2, OnSelect: with(func(s *agent.Session) { a.shareSession(s, false) })},
	}
}
