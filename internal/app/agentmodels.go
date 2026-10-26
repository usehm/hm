package app

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/mirzakhany/yoga/ui"
	"gopkg.in/yaml.v3"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

// providerLabel is how a provider is shown: the two built-in ones by what
// they speak, any other by its config name.
func providerLabel(name string) string {
	switch name {
	case "anthropic":
		return "Anthropic"
	case "openai":
		return "OpenAI-compatible"
	}
	return name
}

// localEndpoint reports whether an OpenAI-compatible provider runs on this
// machine (Ollama, LM Studio), where no key is needed.
func localEndpoint(baseURL string) bool {
	return strings.Contains(baseURL, "localhost") || strings.Contains(baseURL, "127.0.0.1")
}

// providerReady reports whether a provider can be called: it has a key, or
// it is a local endpoint that needs none.
func (a *App) providerReady(name string) bool {
	pc := a.cfg.Current().Agent.Providers[name]
	return a.hasProviderKey(name) || (pc.Type == "openai" && localEndpoint(pc.BaseURL))
}

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

// modelsKey names a provider's model list; a new base URL is a new list.
func (a *App) modelsKey(name string) string {
	return name + "|" + a.cfg.Current().Agent.Providers[name].BaseURL
}

// forgetModels drops a provider's cached list, so it is fetched again.
func (a *App) forgetModels(name string) {
	st := &a.agent
	for k := range st.models {
		if strings.HasPrefix(k, name+"|") {
			delete(st.models, k)
		}
	}
	for k := range st.modelsErr {
		if strings.HasPrefix(k, name+"|") {
			delete(st.modelsErr, k)
		}
	}
}

// modelsFor returns a provider's model list, starting a fetch when it is
// not cached. Body calls it: the fetch runs off the UI goroutine, and a
// failed one is not retried until forgetModels.
func (a *App) modelsFor(name string) (models []string, loading bool, errText string) {
	st := &a.agent
	k := a.modelsKey(name)
	if m, ok := st.models[k]; ok {
		return m, false, ""
	}
	if e, ok := st.modelsErr[k]; ok {
		return nil, false, e
	}
	if st.modelsLoading[k] {
		return nil, true, ""
	}
	if st.modelsLoading == nil {
		st.modelsLoading, st.models, st.modelsErr = map[string]bool{}, map[string][]string{}, map[string]string{}
	}
	st.modelsLoading[k] = true
	pc := a.cfg.Current().Agent.Providers[name]
	go func() {
		var models []string
		p, err := buildProvider(name, pc)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			models, err = p.Models(ctx)
			cancel()
		}
		a.onUI(func() {
			delete(st.modelsLoading, k)
			if err != nil {
				st.modelsErr[k] = err.Error()
				return
			}
			st.models[k] = models
		})
	}()
	return nil, true, ""
}

// modelSetting is the Settings row that picks a provider's model from the
// list its key can use.
func (a *App) modelSetting(name string) ui.FormItem {
	return a.modelPicker("set-agent-model-"+name, name, a.modelOf(name),
		[]string{"agent", "providers", name, "model"}, func(v string) { a.setModel(name, v) })
}

// modelPicker is a Settings row choosing cur from provider name's model
// list; set receives the choice. When the list cannot be read it falls back
// to a text field over the config key.
func (a *App) modelPicker(id, name, cur string, key []string, set func(string)) ui.FormItem {
	models, loading, errText := a.modelsFor(name)
	switch {
	case loading:
		return ui.FormSelect(id, "Model", "Loading the models this key can use…",
			[]ui.SelectOption{{Label: orNone(cur), Value: cur}}, 0, func(string) {})
	case errText != "":
		return ui.FormText(id+"-text", "Model", "Could not list the models ("+errText+"); type the model id",
			a.textValue(key, cur), func(v string) { a.setTextSetting(key, v) })
	}
	opts := make([]ui.SelectOption, 0, len(models)+1)
	sel := -1
	for i, m := range models {
		opts = append(opts, ui.SelectOption{Label: m, Value: m})
		if m == cur {
			sel = i
		}
	}
	if sel < 0 {
		// Keep a configured model the list does not name visible.
		opts = append([]ui.SelectOption{{Label: orNone(cur), Value: cur}}, opts...)
		sel = 0
	}
	return ui.FormSelect(id, "Model", "From the models this key can use", opts, sel, set)
}

func orNone(model string) string {
	if model == "" {
		return "None selected"
	}
	return model
}
