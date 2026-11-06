package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/guard"
	"github.com/mirzakhany/hm/internal/agent/prompts"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
	"github.com/mirzakhany/hm/internal/workspace"
)

// builtinCommands are the slash commands hm runs itself. They win over
// prompt templates of the same name.
var builtinCommands = map[string]bool{"/mode": true, "/provider": true, "/context": true, "/compact": true,
	"/tree": true, "/fork": true, "/label": true, "/export": true, "/share": true, "/new": true, "/clear": true}

// promptTemplates is the prompt templates, read again when stale.
func (a *App) promptTemplates() []prompts.Template {
	st := &a.agent
	if !st.promptsAt.IsZero() {
		return st.prompts
	}
	list, errs := prompts.Load(a.promptDirs())
	st.prompts, st.promptsAt = list, time.Now()
	for _, err := range errs {
		a.notifs.Add("Prompt template "+err.Error(), ui.ToastWarning)
	}
	if st.shadowWarned == nil {
		st.shadowWarned = map[string]bool{}
	}
	for _, t := range list {
		if name := "/" + t.Name; builtinCommands[name] && !st.shadowWarned[name] {
			st.shadowWarned[name] = true
			a.notifs.Add(fmt.Sprintf("Prompt template %s is not used: %s is a built-in command.", t.Path, name), ui.ToastWarning)
		}
	}
	return list
}

func (a *App) promptDirs() []string {
	return prompts.Dirs(a.cfg.Current().Agent.Prompts.Paths, a.ws.Root)
}

// promptsChanged drops the templates when a change touches one of their
// folders, or a folder above one, which is how a new folder shows. Folders
// outside the workspace are not watched; completing a bare / reads them
// again instead.
func (a *App) promptsChanged(ch workspace.Changes) {
	sep := string(filepath.Separator)
	dirs := a.promptDirs()
	touches := func(p string) bool {
		for _, d := range dirs {
			if p == d || strings.HasPrefix(p, d+sep) || strings.HasPrefix(d, p+sep) {
				return true
			}
		}
		return false
	}
	for _, set := range []map[string]bool{ch.Files, ch.Dirs} {
		for p := range set {
			if touches(p) {
				a.agent.promptsAt = time.Time{}
				return
			}
		}
	}
}

// templateFor is the template a slash command names, if any.
func (a *App) templateFor(cmd string) (prompts.Template, bool) {
	for _, t := range a.promptTemplates() {
		if "/"+t.Name == cmd {
			return t, true
		}
	}
	return prompts.Template{}, false
}

// runTemplate expands the template cmd names with args and sends it. It
// reports false when no template has that name.
func (a *App) runTemplate(r *agentRun, cmd, args string) bool {
	t, ok := a.templateFor(cmd)
	if !ok {
		return false
	}
	// Read again so an edit since the list was loaded counts.
	fresh, err := prompts.Read(t.Path)
	if err != nil {
		r.errText = err.Error()
		return true
	}
	if (fresh.Mode != "" || fresh.Model != "") && r.running {
		r.errText = cmd + " switches the mode or model; send it when the agent has finished."
		return true
	}
	if fresh.Mode != "" {
		modes := a.modes()
		if _, ok := modes[fresh.Mode]; !ok {
			r.errText = fmt.Sprintf("%s asks for mode %s, which does not exist. Modes: %s", cmd, fresh.Mode, strings.Join(agent.ModeNames(modes), ", "))
			return true
		}
		r.mode = fresh.Mode
	}
	if fresh.Model != "" {
		if p := a.providerFor(fresh.Model); p != "" {
			r.provider = p
		} else {
			a.toast(fmt.Sprintf("%s asks for model %s, which no provider runs; using %s.", cmd, fresh.Model, providerLabel(r.provider)), ui.ToastWarning)
		}
	}
	text, warnings := fresh.Expand(args, a.promptInclude)
	for _, w := range warnings {
		a.toast(cmd+": "+w, ui.ToastWarning)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		r.errText = cmd + " is empty."
		return true
	}
	text = a.expandContext(text)
	if r.from == nil {
		r.from = map[string]string{}
	}
	r.from[text] = cmd
	a.sendText(r, text, false)
	return true
}

// providerFor is the provider a template's model names: a provider by its
// name, else the first whose configured model it is.
func (a *App) providerFor(model string) string {
	names := a.providerNames()
	for _, p := range names {
		if p == model {
			return p
		}
	}
	for _, p := range names {
		if a.modelOf(p) == model {
			return p
		}
	}
	return ""
}

// promptInclude reads the workspace file an @path line in a template
// names, under the same guard.yaml rules as the agent's reads.
func (a *App) promptInclude(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", errors.New("only workspace paths can be included")
	}
	path := filepath.Join(a.ws.Root, filepath.FromSlash(rel))
	if !inside(a.ws.Root, path) {
		return "", errors.New("only workspace paths can be included")
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		root, _ := filepath.EvalSymlinks(a.ws.Root)
		if !inside(root, real) {
			return "", errors.New("links outside the workspace")
		}
	}
	grd, err := guard.Load(userGuardPath(), projectGuardPath(a.ws.Root), a.ws.Root)
	if err != nil {
		return "", errors.New("guard.yaml: " + err.Error())
	}
	if v, why := grd.Check("read", tools.Plan{Category: tools.CatRead, Paths: []string{path}}); v == guard.Deny {
		return "", errors.New(why)
	}
	st, err := os.Stat(path)
	switch {
	case err != nil:
		return "", errors.New("no such file")
	case st.IsDir():
		return "", errors.New("is a folder")
	case st.Size() >= 200<<10:
		return "", errors.New("is too large to include")
	}
	if ed := a.bufs.Get(path); ed != nil {
		return string(ed.Bytes()), nil
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

// inside reports whether path is root or under it.
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// templateOf is the command a user message was expanded from, if any. A
// message queued during a turn is one text block among others.
func (r *agentRun) templateOf(m provider.Message) string {
	for _, b := range m.Blocks {
		if b.Type == provider.Text {
			if cmd := r.from[b.Text]; cmd != "" {
				return cmd
			}
		}
	}
	return ""
}

// templateSuggestions lists the templates whose command starts with word.
func (a *App) templateSuggestions(word string) []ui.Suggestion {
	if word == "/" && time.Since(a.agent.promptsAt) > 2*time.Second {
		a.agent.promptsAt = time.Time{}
	}
	var out []ui.Suggestion
	for _, t := range a.promptTemplates() {
		cmd := "/" + t.Name
		if builtinCommands[cmd] || !strings.HasPrefix(cmd, word) {
			continue
		}
		label := cmd
		if t.ArgumentHint != "" {
			label += " " + t.ArgumentHint
		}
		detail := t.Description
		if t.Group != "" {
			detail = strings.TrimSpace(t.Group + " · " + detail)
		}
		out = append(out, ui.Suggestion{Label: label, Detail: detail, Insert: cmd + " "})
	}
	return out
}
