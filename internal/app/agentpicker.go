// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/agent"
	"github.com/usehm/hm/internal/agent/provider"
)

// scopeModels is the model picker's list in the palette.
const scopeModels = "models"

// modelChoice is a model the picker offers: a provider and one of its
// models.
type modelChoice struct {
	prov, model string
}

func (m modelChoice) String() string { return m.prov + "/" + m.model }

// splitModel reads provider/model; the model may hold more slashes, as
// OpenRouter's do.
func splitModel(s string) (modelChoice, bool) {
	p, m, ok := strings.Cut(s, "/")
	return modelChoice{p, m}, ok && p != "" && m != ""
}

// runModel is the model r's next turn runs: the one picked for the
// session, else its provider's.
func (a *App) runModel(r *agentRun) string {
	if r.model != "" {
		return r.model
	}
	return a.modelOf(r.provider)
}

// runEffort is how hard r's next turn thinks: /think's word for the
// session, else the provider's; empty for the model's default.
func (a *App) runEffort(r *agentRun) string {
	if r.effort != "" {
		return r.effort
	}
	return a.cfg.Current().Agent.Providers[r.provider].Effort
}

// modelLabel is the model and effort the composer shows.
func (a *App) modelLabel(r *agentRun) string {
	m := a.runModel(r)
	if e := a.runEffort(r); e != "" && m != "" {
		m += " · " + e
	}
	return m
}

// restoreModel takes the provider, model and effort s last ran with, at
// its head. A model or effort that is the provider's own is left to
// follow the config.
func (a *App) restoreModel(r *agentRun, s *agent.Session) {
	if s.Provider == "" {
		return
	}
	r.provider, r.model, r.effort = s.Provider, "", ""
	pc := a.cfg.Current().Agent.Providers[s.Provider]
	if s.Model != a.modelOf(s.Provider) {
		r.model = s.Model
	}
	if s.Effort != pc.Effort {
		r.effort = s.Effort
	}
}

// favoriteModels are agent.favorite_models whose provider is set up.
func (a *App) favoriteModels() []modelChoice {
	var out []modelChoice
	for _, f := range a.cfg.Current().Agent.FavoriteModels {
		if m, ok := splitModel(f); ok && a.providerReady(m.prov) {
			out = append(out, m)
		}
	}
	return out
}

// modelChoices lists the models of every provider that is set up:
// favourites first, then by provider its configured model, the models its
// API listed (once read; loadModelLists reads them) and its models.json
// entries.
func (a *App) modelChoices() []modelChoice {
	seen := map[modelChoice]bool{}
	var out []modelChoice
	add := func(m modelChoice) {
		if m.model != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, f := range a.favoriteModels() {
		add(f)
	}
	reg := a.modelRegistry()
	for _, p := range a.providerNames() {
		if !a.providerReady(p) {
			continue
		}
		add(modelChoice{p, a.modelOf(p)})
		for _, m := range a.agent.models[a.modelsKey(p)] {
			add(modelChoice{p, m})
		}
		for _, m := range reg.Listed(p) {
			add(modelChoice{p, m.ID})
		}
	}
	return out
}

// loadModelLists starts reading the model list of every provider that is
// set up and not read yet.
func (a *App) loadModelLists() (loading bool) {
	for _, p := range a.providerNames() {
		if a.providerReady(p) {
			if _, l, _ := a.modelsFor(p); l {
				loading = true
			}
		}
	}
	return loading
}

// openModelPicker lists every provider's models in the palette.
func (a *App) openModelPicker(c *ui.Ctx) {
	if a.run() == nil {
		return
	}
	a.agent.picking = true
	a.loadModelLists()
	c.Commands().ShowScope(scopeModels, "Pick a model for this session")
}

// modelItems are the picker's entries, while it is open.
func (a *App) modelItems(c *ui.Ctx) []*ui.Command {
	st := &a.agent
	if !st.picking {
		return nil
	}
	r := a.run()
	if !c.Commands().Open || r == nil {
		st.picking = false
		return nil
	}
	favs := a.favoriteModels()
	cur := modelChoice{r.provider, a.runModel(r)}
	reg := a.modelRegistry()
	var out []*ui.Command
	for _, m := range a.modelChoices() {
		info, _ := reg.Lookup(m.prov, m.model)
		icon := icons.Cpu
		if slices.Contains(favs, m) {
			icon = icons.Star
		}
		trailing := ""
		if m == cur {
			trailing = "current"
		}
		out = append(out, ui.Item("model."+m.String()).Title(m.model).Detail(modelDetail(providerLabel(m.prov), info)).
			Icon(icon).Trailing(trailing).Scope(scopeModels).Run(func() { a.setRunModel(r, m) }))
	}
	if a.loadModelLists() {
		out = append(out, ui.Item("model.loading").Title("Reading the providers' model lists…").
			Icon(icons.RefreshCw).Enabled(false).Scope(scopeModels))
	}
	return out
}

// modelDetail is a picker entry's second line: the provider, then what is
// known of the model.
func modelDetail(prov string, m provider.ModelInfo) string {
	parts := []string{prov}
	if m.Name != "" && m.Name != m.ID {
		parts = append(parts, m.Name)
	}
	if m.ContextWindow > 0 {
		parts = append(parts, tokens(int64(m.ContextWindow))+" context")
	}
	if p := m.Price; p.Input > 0 || p.Output > 0 {
		parts = append(parts, fmt.Sprintf("$%g / $%g per Mtok", p.Input, p.Output))
	}
	return strings.Join(parts, " · ")
}

// setRunModel makes m the model of r's next turns. The session's history
// goes along in neutral form; another provider's thinking is left out.
func (a *App) setRunModel(r *agentRun, m modelChoice) {
	r.provider, r.model, r.errText = m.prov, m.model, ""
	if m.model == a.modelOf(m.prov) {
		r.model = "" // the provider's own: follow it when Settings changes it
	}
	msg := "Model: " + m.model + " (" + providerLabel(m.prov) + ")"
	if r.running {
		msg += ", from the next turn"
	}
	a.toast(msg, ui.ToastInfo)
}

// cycleModel moves r to the next of agent.favorite_models.
func (a *App) cycleModel(r *agentRun) {
	favs := a.favoriteModels()
	if len(favs) == 0 {
		a.toast("Add models to agent.favorite_models, as provider/model, to step through them", ui.ToastInfo)
		return
	}
	i := slices.Index(favs, modelChoice{r.provider, a.runModel(r)})
	a.setRunModel(r, favs[(i+1)%len(favs)])
}

// resolveModel finds the model /model names: provider/model, a provider's
// name, a model id, or a part of one that only one model has. Among
// several matches the session's provider wins.
func (a *App) resolveModel(r *agentRun, q string) (modelChoice, error) {
	q = strings.TrimSpace(q)
	provs := a.cfg.Current().Agent.Providers
	if m, ok := splitModel(q); ok {
		if _, known := provs[m.prov]; known {
			return m, nil
		}
	}
	if _, ok := provs[q]; ok {
		return modelChoice{q, a.modelOf(q)}, nil
	}
	all := a.modelChoices()
	pick := func(ms []modelChoice) (modelChoice, bool) {
		if len(ms) == 1 {
			return ms[0], true
		}
		var mine []modelChoice
		for _, m := range ms {
			if m.prov == r.provider {
				mine = append(mine, m)
			}
		}
		if len(mine) == 1 {
			return mine[0], true
		}
		return modelChoice{}, false
	}
	var exact, part []modelChoice
	low := strings.ToLower(q)
	for _, m := range all {
		switch {
		case m.model == q:
			exact = append(exact, m)
		case strings.Contains(strings.ToLower(m.String()), low):
			part = append(part, m)
		}
	}
	for _, ms := range [][]modelChoice{exact, part} {
		if m, ok := pick(ms); ok {
			return m, nil
		}
		if len(ms) > 1 {
			names := make([]string, 0, 5)
			for _, m := range ms[:min(5, len(ms))] {
				names = append(names, m.String())
			}
			more := ""
			if len(ms) > 5 {
				more = fmt.Sprintf(" and %d more", len(ms)-5)
			}
			return modelChoice{}, fmt.Errorf("%s matches %s%s; name one as provider/model", q, strings.Join(names, ", "), more)
		}
	}
	if a.loadModelLists() {
		return modelChoice{}, fmt.Errorf("No model matches %s yet; the model lists are still being read, try again", q)
	}
	return modelChoice{}, fmt.Errorf("No model matches %s; /model alone lists them", q)
}

// modelCommand runs /model [name].
func (a *App) modelCommand(r *agentRun, arg string) {
	if arg == "" {
		a.openModelPicker(a.ctx)
		return
	}
	m, err := a.resolveModel(r, arg)
	if err != nil {
		r.errText = err.Error()
		return
	}
	a.setRunModel(r, m)
}

// thinkCommand runs /think [effort].
func (a *App) thinkCommand(r *agentRun, arg string) {
	words := strings.Join(provider.Efforts, ", ")
	if arg == "" {
		cur := a.runEffort(r)
		if cur == "" {
			cur = "the model's default"
		}
		r.errText, r.info = "", "Thinking: "+cur+". /think "+strings.ReplaceAll(words, ", ", "|")+" sets it for this session; /think default goes back to the provider's."
		return
	}
	if arg == "default" {
		r.effort, r.errText = "", ""
		a.toast("Thinking: the provider's", ui.ToastInfo)
		return
	}
	if !slices.Contains(provider.Efforts, arg) {
		r.errText = "Unknown effort " + arg + ". Use one of " + words + ", or default"
		return
	}
	if info, _ := a.modelRegistry().Lookup(r.provider, a.runModel(r)); len(info.Efforts) > 0 && arg != "off" &&
		!slices.Contains(info.Efforts, arg) {
		r.errText = a.runModel(r) + " takes " + strings.Join(info.Efforts, ", ")
		return
	}
	r.effort, r.errText = arg, ""
	a.toast("Thinking: "+arg, ui.ToastInfo)
}
