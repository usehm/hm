package app

import (
	"context"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

// scopeModels lists the models of the provider being picked for.
const scopeModels = "models"

// modelOf is the model a provider runs, with the Anthropic default filled in.
func (a *App) modelOf(name string) string {
	pc := a.cfg.Current().Agent.Providers[name]
	if pc.Model == "" && pc.Type == "anthropic" {
		return provider.DefaultAnthropicModel
	}
	return pc.Model
}

// setModel makes model the provider's model. It goes to the user config,
// or to the project config when that one pins the provider's model.
func (a *App) setModel(name, model string) {
	if model == "" || model == a.cfg.Current().Agent.Providers[name].Model {
		return
	}
	a.writeSetting(a.projectSetsModel(name), []string{"agent", "providers", name, "model"}, model)
	a.toast("Agent model: "+model, ui.ToastInfo)
}

// projectSetsModel reports whether the project config sets a provider's model.
func (a *App) projectSetsModel(name string) bool {
	_, proj := a.cfg.Paths()
	data, err := os.ReadFile(proj)
	if err != nil {
		return false
	}
	var c struct {
		Agent struct {
			Providers map[string]struct {
				Model *string `yaml:"model"`
			} `yaml:"providers"`
		} `yaml:"agent"`
	}
	return yaml.Unmarshal(data, &c) == nil && c.Agent.Providers[name].Model != nil
}

// pickModel opens the model list for a provider, fetching it first when it
// is not cached.
func (a *App) pickModel(name string) {
	st := &a.agent
	st.pickFor = name
	if _, ok := st.models[name]; ok {
		a.ctx.Commands().ShowScope(scopeModels, "Select a model for "+name)
		return
	}
	a.loadModels(name, true)
}

// loadModels fetches a provider's model list in the background, and shows
// the picker when it arrives if show is set.
func (a *App) loadModels(name string, show bool) {
	st := &a.agent
	pc, ok := a.cfg.Current().Agent.Providers[name]
	if !ok || st.modelsLoading[name] {
		return
	}
	if st.modelsLoading == nil {
		st.modelsLoading = map[string]bool{}
	}
	st.modelsLoading[name] = true
	go func() {
		var models []string
		p, err := buildProvider(name, pc)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			models, err = p.Models(ctx)
			cancel()
		}
		a.onUI(func() {
			delete(st.modelsLoading, name)
			if err != nil {
				a.fail("Could not list the "+name+" models; set one with /model <id>", err)
				return
			}
			if st.models == nil {
				st.models = map[string][]string{}
			}
			st.models[name] = models
			if show && st.pickFor == name && a.ctx != nil {
				a.ctx.Commands().ShowScope(scopeModels, "Select a model for "+name)
			}
		})
	}()
}

// modelCommands are the picker's entries for the provider being picked for.
func (a *App) modelCommands() []*ui.Command {
	st := &a.agent
	name := st.pickFor
	cur := a.modelOf(name)
	var out []*ui.Command
	for _, m := range st.models[name] {
		m := m
		it := ui.Item("model." + name + "." + m).Title(m).Icon(icons.Bot).Scope(scopeModels).Run(func() { a.setModel(name, m) })
		if m == cur {
			it = it.Detail("current")
		}
		out = append(out, it)
	}
	return out
}

// modelButton shows a provider's model and opens the picker.
func (a *App) modelButton(id, name string) *ui.Node {
	label := a.modelOf(name)
	if label == "" {
		label = "Select a model"
	}
	if a.agent.modelsLoading[name] {
		label = "Loading models…"
	}
	return ui.Button(id, ui.Text(label).Ellipsis(ui.EllipsisEnd).Grow(1)).IconStart(icons.Bot).IconEnd(icons.ChevronDown).
		Tooltip("Pick from the models this key can use").OnClick(func() { a.pickModel(name) })
}
