package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/shape"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/config"
)

// uiFontSize is the chrome's text size; editor.font_size sizes code.
const uiFontSize = 14

// appliedConfig remembers what was last pushed into yoga, so a reload only
// redoes what changed (SetFont rebuilds the glyph atlas).
type appliedConfig struct {
	theme string
	font  shape.FontConfig
	ok    bool
}

// applyConfig pushes cfg into the theme, fonts, editor defaults and tree.
func (a *App) applyConfig(cfg config.Config) {
	ed := cfg.Editor
	if ed.Theme != a.applied.theme {
		theme.Use(ed.Theme)
		a.applied.theme = ed.Theme
	}
	font := shape.FontConfig{
		UI:       shape.FaceConfig{Size: uiFontSize},
		Mono:     shape.FaceConfig{Size: float32(ed.FontSize), Family: ed.FontFamily},
		TabWidth: ed.TabWidth,
	}
	if !a.applied.ok || font != a.applied.font {
		if err := yoga.SetFont(font); err != nil {
			a.notifs.Add("Font: "+err.Error(), ui.ToastWarning)
		}
		a.applied.font = font
	}
	a.applied.ok = true
	ui.EditorDefaults.SoftWrap = ed.SoftWrap
	ui.EditorDefaults.LineNumbers = ed.LineNumbers
	ui.EditorDefaults.IndentSpaces = ed.InsertSpaces
	a.lang.Apply(cfg.Languages)
	if a.tree != nil {
		hidden := ed.ShowHidden
		a.tree.Hidden = func(name string, dir bool) bool {
			return hideEntry(name, dir) || (!hidden && len(name) > 0 && name[0] == '.')
		}
		a.tree.Reload(a.ws.Root)
	}
}

// reloadConfig re-reads the config files after one changed. A valid result
// is applied live; a broken one keeps the last good config and shows where
// the problem is.
func (a *App) reloadConfig() {
	before := a.cfg.Current()
	beforeProblems := a.problemsKey
	applied, problems := a.cfg.Reload()
	a.problemsKey = fmt.Sprint(problems)
	a.showConfigDiagnostics()
	if !applied {
		if a.problemsKey != beforeProblems {
			msg := fmt.Sprintf("Settings not applied: %s", problems[0].Error())
			if len(problems) > 1 {
				msg += fmt.Sprintf(" (and %d more)", len(problems)-1)
			}
			a.toast(msg, ui.ToastError)
		}
		return
	}
	cfg := a.cfg.Current()
	if reflect.DeepEqual(before, cfg) && beforeProblems == a.problemsKey {
		return
	}
	a.applyConfig(cfg)
	a.toast("Settings applied", ui.ToastSuccess)
}

// showConfigDiagnostics marks config problems in open config file tabs.
func (a *App) showConfigDiagnostics() {
	user, project := a.cfg.Paths()
	problems := a.cfg.Problems()
	for _, p := range []string{user, project} {
		ed := a.bufs.Get(p)
		if ed == nil {
			continue
		}
		var diags []lsp.Diagnostic
		for _, pr := range problems {
			if pr.File != p {
				continue
			}
			line, col := max(pr.Line-1, 0), max(pr.Col-1, 0)
			diags = append(diags, lsp.Diagnostic{
				Range: lsp.Range{
					Start: lsp.Position{Line: line, Character: col},
					End:   lsp.Position{Line: line, Character: 1 << 20},
				},
				Severity: lsp.SeverityError,
				Source:   "hm",
				Message:  pr.Msg,
			})
		}
		ed.SetDiagnostics(diags)
	}
}

// key returns the chord bound to a command id.
func (a *App) key(id string) string { return a.cfg.Current().Keys[id] }

// configFile returns the config file for a scope, creating it from the
// template so it can be opened.
func (a *App) configFile(project bool) (string, error) {
	user, proj := a.cfg.Paths()
	path := user
	if project {
		path = proj
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, config.UserTemplate(), 0o644); err != nil {
			return "", err
		}
	}
	return path, nil
}

// openConfigYAML opens a scope's config file in an editor tab.
func (a *App) openConfigYAML(project bool) {
	path, err := a.configFile(project)
	if err != nil {
		a.fail("Could not create the config file", err)
		return
	}
	if _, err := a.ws.Open(path); err != nil {
		a.fail("Open failed", err)
		return
	}
	a.showConfigDiagnostics()
}

// openDefaults writes the built-in defaults next to the user config and opens
// them, for reference.
func (a *App) openDefaults() {
	path := filepath.Join(config.UserDir(), "defaults.yaml")
	head := "# Built-in defaults, regenerated each time hm opens this file. Edits here\n# do nothing: copy keys into config.yaml instead.\n"
	if a.bufs.Get(path) == nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			err = os.WriteFile(path, append([]byte(head), config.DefaultsYAML()...), 0o644)
			a.fail("Could not write the defaults", err)
		}
	}
	if _, err := a.ws.Open(path); err != nil {
		a.fail("Open failed", err)
	}
}

// setSetting writes one setting to the settings page's scope and reloads.
func (a *App) setSetting(key []string, value any) { a.writeSetting(a.settingsProject, key, value) }

// setUserSetting writes one setting to the user file, whatever the page shows.
func (a *App) setUserSetting(key []string, value any) { a.writeSetting(false, key, value) }

func (a *App) writeSetting(project bool, key []string, value any) {
	user, proj := a.cfg.Paths()
	path := user
	if project {
		path = proj
	}
	if err := config.SetValue(path, key, value); err != nil {
		a.fail("Could not save the setting", err)
		return
	}
	a.reloadBuffer(path)
	a.reloadConfig()
}

// reloadBuffer refreshes an open, unmodified buffer after hm wrote its file.
func (a *App) reloadBuffer(path string) {
	ed := a.bufs.Get(path)
	if ed == nil || ed.Modified() {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	ed.SetText(string(data))
	ed.MarkSaved()
}

// pendingText is a text setting being typed; it is written once typing pauses.
type pendingText struct {
	key   []string
	value string
	at    time.Time
	conv  func(string) any // nil writes the text as is
}

const textSettingDelay = 700 * time.Millisecond

func (a *App) setTextSetting(key []string, value string) {
	a.pendingText = &pendingText{key: key, value: value, at: time.Now()}
}

// setListSetting is setTextSetting for a comma-separated list.
func (a *App) setListSetting(key []string, value string) {
	a.pendingText = &pendingText{key: key, value: value, at: time.Now(), conv: splitList}
}

func splitList(s string) any {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// flushPending writes a paused text setting and runs a delayed auto save.
func (a *App) flushPending(c *ui.Ctx) {
	if p := a.pendingText; p != nil {
		if wait := textSettingDelay - time.Since(p.at); wait > 0 {
			c.Animate(wait)
		} else {
			a.pendingText = nil
			var v any = p.value
			if p.conv != nil {
				v = p.conv(p.value)
			}
			a.setSetting(p.key, v)
		}
	}
	a.autoSave(c)
}

// textValue shows a pending text setting while it is being typed.
func (a *App) textValue(key []string, current string) string {
	if p := a.pendingText; p != nil && reflect.DeepEqual(p.key, key) {
		return p.value
	}
	return current
}

// autoSave runs editor.auto_save. "delay" saves files once edits pause;
// "focus_change" saves the previous tab when another becomes active.
func (a *App) autoSave(c *ui.Ctx) {
	ed := a.cfg.Current().Editor
	switch ed.AutoSave {
	case config.AutoSaveDelay:
		now := time.Now()
		for i, t := range a.ws.Tabs {
			e := a.bufs.Get(t.Path)
			if e == nil || !a.ws.Modified(i) {
				continue
			}
			rev := e.Revision()
			seen, ok := a.lastEdit[t.Path]
			if !ok || seen.rev != rev {
				a.lastEdit[t.Path] = editSeen{rev: rev, at: now}
				c.Animate(ed.AutoSaveDelay.Duration)
				continue
			}
			if wait := ed.AutoSaveDelay.Duration - now.Sub(seen.at); wait > 0 {
				c.Animate(wait)
				continue
			}
			a.fail("Auto save failed", a.saveFile(t.Path))
		}
	case config.AutoSaveFocusChange:
		cur := ""
		if t := a.ws.ActiveTab(); t != nil {
			cur = t.Path
		}
		if a.lastActive != "" && a.lastActive != cur {
			if e := a.bufs.Get(a.lastActive); e != nil && e.Modified() {
				a.fail("Auto save failed", a.saveFile(a.lastActive))
			}
		}
		a.lastActive = cur
	}
}

type editSeen struct {
	rev uint64
	at  time.Time
}
