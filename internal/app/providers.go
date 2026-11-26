// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/yogaui/yoga/icons"
	"github.com/yogaui/yoga/ui"
	"gopkg.in/yaml.v3"

	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/config"
)

// Settings › Providers lists the providers set up, opens one to change its
// key, model and endpoint, and adds new ones from a dialog. Providers are
// written to the user file: their keys are the user's.

// defaultProviders are the names the built-in config defines.
var defaultProviders = sync.OnceValue(func() map[string]bool {
	var c struct {
		Agent struct {
			Providers map[string]any `yaml:"providers"`
		} `yaml:"agent"`
	}
	_ = yaml.Unmarshal(config.DefaultsYAML(), &c)
	out := map[string]bool{}
	for n := range c.Agent.Providers {
		out[n] = true
	}
	return out
})

// listedProviders are the providers the page shows: each one the user
// defined, and a built-in one once it can be called. A fresh install lists
// none.
func (a *App) listedProviders() []string {
	var out []string
	for n := range a.cfg.Current().Agent.Providers {
		if !defaultProviders()[n] || a.providerReady(n) {
			out = append(out, n)
		}
	}
	sortStrings(out)
	return out
}

// providerTypes are what the add dialog offers, in its order.
func providerTypes() []string {
	return append([]string{"anthropic", "openai", "google", "azure"}, provider.PresetNames()...)
}

// typeLabel names a provider type in the add dialog and on the cards.
func typeLabel(typ string) string {
	if typ == "openai" || typ == "" {
		return "OpenAI or compatible"
	}
	return providerLabel(typ)
}

// noKeyNeeded reports whether pc signs in without an API key.
func noKeyNeeded(pc config.Provider) bool { return localProvider(pc) || vertexProvider(pc) }

// providerState is a provider's connection as the page shows it.
type providerState struct {
	label  string
	tone   ui.BadgeTone
	models []string
	err    string
}

// providerStatus reads name's connection off its model list, fetching the
// list when it is not cached.
func (a *App) providerStatus(name string) providerState {
	pc := a.cfg.Current().Agent.Providers[name]
	switch {
	case pc.Disabled:
		return providerState{label: "Disabled", tone: ui.BadgeMuted}
	case !a.providerReady(name):
		return providerState{label: "Not configured", tone: ui.BadgeWarning}
	}
	models, loading, errText := a.modelsFor(name)
	switch {
	case loading:
		return providerState{label: "Checking…", tone: ui.BadgeMuted}
	case errText != "":
		return providerState{label: "Error", tone: ui.BadgeError, err: errText}
	}
	return providerState{label: "Connected", tone: ui.BadgeSuccess, models: models}
}

func (a *App) keyStatus(name string) (string, ui.BadgeTone) {
	switch {
	case a.hasProviderKey(name):
		return "Key set", ui.BadgeSuccess
	case noKeyNeeded(a.cfg.Current().Agent.Providers[name]):
		return "No key needed", ui.BadgeMuted
	}
	return "Key missing", ui.BadgeWarning
}

// providersSection is Settings › Providers: the list, or one provider.
func (a *App) providersSection(c *ui.Ctx) ui.View {
	if _, ok := a.cfg.Current().Agent.Providers[a.settingsProvider]; ok {
		return a.providerDetail(c, a.settingsProvider)
	}
	a.settingsProvider = ""
	th := c.Theme()
	add := ui.Button("prov-add", ui.Text("Add provider")).Primary().IconStart(icons.Plus).
		OnClick(func() { a.showAddProvider(&newProvider{}) })
	names := a.listedProviders()
	if len(names) == 0 {
		return ui.Column(
			ui.ViewOf(ui.EmptyState("No providers yet",
				"Add Anthropic, OpenAI, Gemini or any OpenAI-compatible service to give the agent a model.").
				EmptyIcon(icons.Plug)),
			ui.Row(add).Justify(ui.JustifyCenter),
		).Gap(th.Spacing.M)
	}
	rows := []ui.View{ui.Row(
		ui.Muted("Keys are kept in "+keychainName()+"; the rest goes to the user config.").Grow(1).Shrink(1),
		add,
	).Gap(th.Spacing.M).Align(ui.AlignCenter)}
	for _, n := range names {
		rows = append(rows, a.providerCard(c, n))
	}
	return ui.Column(rows...).Gap(th.Spacing.S)
}

// providerCard is a provider's row in the list; a click opens it.
func (a *App) providerCard(c *ui.Ctx, name string) ui.View {
	th := c.Theme()
	cfg := a.cfg.Current().Agent
	pc := cfg.Providers[name]
	st := a.providerStatus(name)
	keyLabel, keyTone := a.keyStatus(name)
	badges := []ui.View{
		ui.Badge(st.label).Tone(st.tone),
		ui.Badge(keyLabel).Tone(keyTone),
	}
	if st.models != nil {
		badges = append(badges, ui.Badge(fmt.Sprintf("%d %s", len(st.models), plural(len(st.models), "model"))).Tone(ui.BadgeMuted))
	}
	sub := typeLabel(pc.Type)
	if name == cfg.DefaultProvider {
		sub += " · default provider"
	}
	open := func() { a.settingsProvider = name }
	return ui.Card(providerLabel(name), sub, ui.Column(
		ui.Row(badges...).Gap(th.Spacing.XS).Wrap(),
		ui.Caption("Model: "+orNone(a.modelOf(name))).Ellipsis(ui.EllipsisEnd),
	).Gap(th.Spacing.XS)).Flat().
		Actions(
			ui.IconButton("prov-edit-"+name, icons.Pencil).Tooltip("Edit").OnClick(open),
			ui.IconButton("prov-del-"+name, icons.Trash2).Tooltip("Delete").OnClick(func() { a.confirmDeleteProvider(name) }),
		).OnClick(open)
}

// providerDetail edits one provider: whether it is on, its endpoint, key
// and default model, and checks that it answers.
func (a *App) providerDetail(c *ui.Ctx, name string) ui.View {
	th := c.Theme()
	pc := a.cfg.Current().Agent.Providers[name]
	st := a.providerStatus(name)
	keyLabel, _ := a.keyStatus(name)
	on := []string{"agent", "providers", name, "disabled"}
	items := []ui.FormItem{
		ui.FormHeading(providerLabel(name), typeLabel(pc.Type)+" · "+keyLabel),
		ui.FormSwitch("prov-enabled", "Enabled", "Off keeps it set up but out of the model pickers",
			!pc.Disabled, func(v bool) { a.setUserSetting(on, !v) }),
	}
	items = append(items, a.providerFields(name, pc)...)
	if a.providerReady(name) && !pc.Disabled {
		m := a.modelSetting(name)
		m.Label = "Default model"
		items = append(items, m)
	}
	var status ui.View
	switch {
	case st.err != "":
		status = ui.Alert("Could not reach "+providerLabel(name)+": "+st.err, ui.AlertError)
	case st.models != nil:
		status = ui.Alert(fmt.Sprintf("Connected: %d %s available", len(st.models), plural(len(st.models), "model")), ui.AlertSuccess)
	case st.label == "Checking…":
		status = ui.Alert("Checking the connection…", ui.AlertInfo)
	}
	keyText := "Add key"
	if a.hasProviderKey(name) {
		keyText = "Replace key"
	}
	buttons := []ui.View{
		ui.Button("prov-key", ui.Text(keyText)).IconStart(icons.KeyRound).OnClick(func() { a.askProviderKey(name) }),
	}
	if a.hasProviderKey(name) {
		buttons = append(buttons, ui.Button("prov-key-remove", ui.Text("Remove key")).Ghost().
			OnClick(func() { a.removeProviderKey(name) }))
	}
	buttons = append(buttons,
		ui.Button("prov-test", ui.Text("Test connection")).IconStart(icons.PlugZap).
			Disabled(!a.providerReady(name) || pc.Disabled).OnClick(func() { a.forgetModels(name) }),
		ui.Spacer(),
		ui.Button("prov-delete", ui.Text("Delete")).Ghost().IconStart(icons.Trash2).
			OnClick(func() { a.confirmDeleteProvider(name) }),
	)
	col := []ui.View{
		ui.Row(ui.Button("prov-back", ui.Text("All providers")).Ghost().IconStart(icons.ArrowLeft).
			OnClick(func() { a.settingsProvider = "" })),
	}
	if status != nil {
		col = append(col, status)
	}
	col = append(col,
		ui.Form("prov-form-"+name, items...).Grouped(),
		ui.Row(buttons...).Gap(th.Spacing.S).Wrap(),
	)
	return ui.Column(col...).Gap(th.Spacing.M)
}

// providerFields are the endpoint settings pc's type takes.
func (a *App) providerFields(name string, pc config.Provider) []ui.FormItem {
	key := func(k string) []string { return []string{"agent", "providers", name, k} }
	var items []ui.FormItem
	if provider.OpenAICompatible(pc.Type) || pc.Type == "azure" {
		hint := "OpenAI, OpenRouter, Ollama, LM Studio, Groq, DeepSeek, …"
		if p, ok := provider.Presets[pc.Type]; ok {
			hint = "Empty uses " + p.BaseURL
		} else if pc.Type == "azure" {
			hint = "Your resource: https://<resource>.openai.azure.com"
		}
		items = append(items, ui.FormText("prov-base-"+name, "Base URL", hint,
			a.textValue(key("base_url"), pc.BaseURL), func(v string) { a.setTextSetting(key("base_url"), v) }))
	}
	switch pc.Type {
	case "azure":
		items = append(items, ui.FormText("prov-apiver-"+name, "API version",
			"Such as 2024-10-21, with the deployment as the model; empty uses the v1 API",
			a.textValue(key("api_version"), pc.APIVersion), func(v string) { a.setTextSetting(key("api_version"), v) }))
	case "google":
		items = append(items,
			ui.FormText("prov-project-"+name, "Vertex AI project", "Empty uses the Gemini API with a key; set, signs in with gcloud auth application-default login",
				a.textValue(key("project"), pc.Project), func(v string) { a.setTextSetting(key("project"), v) }),
			ui.FormText("prov-location-"+name, "Vertex AI location", "A region such as us-central1; empty is global",
				a.textValue(key("location"), pc.Location), func(v string) { a.setTextSetting(key("location"), v) }))
	}
	return items
}

// removeProviderKey deletes name's key from the keychain.
func (a *App) removeProviderKey(name string) {
	if err := a.agent.keys.Delete(name); err != nil {
		a.fail("Could not remove the key", err)
		return
	}
	a.refreshProviderKeys()
	a.forgetModels(name)
	a.compl.forgetProvider()
}

func (a *App) confirmDeleteProvider(name string) {
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title: "Delete " + providerLabel(name) + "?", Width: 440, FitContent: true,
		Body: func(c *ui.Ctx) ui.View {
			return ui.Paragraph("This removes agent.providers." + name + " from the user config and its key from " +
				keychainName() + ".").Padding(c.Theme().Spacing.L)
		},
		Actions: []ui.DialogAction{
			{Label: "Cancel"},
			{Label: "Delete", Danger: true, OnClick: func() { a.deleteProvider(name) }},
		},
	})
}

// deleteProvider removes name from the user config and the keychain. A
// built-in provider falls back to its unconfigured default, off the list.
func (a *App) deleteProvider(name string) {
	user, proj := a.cfg.Paths()
	if err := config.Unset(user, []string{"agent", "providers", name}); err != nil {
		a.fail("Could not delete the provider", err)
		return
	}
	_ = a.agent.keys.Delete(name)
	a.settingsProvider = ""
	a.reloadBuffer(user)
	a.reloadConfig()
	a.refreshProviderKeys()
	a.forgetModels(name)
	a.compl.forgetProvider()
	if _, ok := a.cfg.Current().Agent.Providers[name]; ok && !defaultProviders()[name] {
		a.toast(name+" is also set in "+a.ws.Rel(proj), ui.ToastInfo)
	}
}

// newProvider is the add dialog's form.
type newProvider struct {
	typ, name, baseURL, key string
	named                   bool // the name was typed, not taken from the type
	problem                 string
}

var providerNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// takenName reports whether name is in use. A built-in provider not set
// up yet is free for its own type.
func (a *App) takenName(name, typ string) bool {
	pc, ok := a.cfg.Current().Agent.Providers[name]
	return ok && !(defaultProviders()[name] && !a.providerReady(name) && pc.Type == typ)
}

// suggestName is typ, numbered past the names in use.
func (a *App) suggestName(typ string) string {
	name := typ
	for i := 2; a.takenName(name, typ); i++ {
		name = fmt.Sprintf("%s-%d", typ, i)
	}
	return name
}

// showAddProvider opens the add dialog over np; a problem on save opens it
// again with what was typed.
func (a *App) showAddProvider(np *newProvider) {
	if np.typ == "" {
		np.typ = providerTypes()[0]
	}
	if !np.named {
		np.name = a.suggestName(np.typ)
	}
	types := providerTypes()
	opts := make([]ui.SelectOption, len(types))
	for i, t := range types {
		opts[i] = ui.SelectOption{Label: typeLabel(t), Value: t}
	}
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title: "Add provider", Width: 520, FitContent: true,
		Body: func(c *ui.Ctx) ui.View {
			th := c.Theme()
			pc := config.Provider{Type: np.typ, BaseURL: np.baseURL}
			items := []ui.FormItem{
				ui.FormSelect("add-prov-type", "Service", "What the provider speaks, or the service it is",
					opts, slices.Index(types, np.typ), func(v string) {
						np.typ = v
						if !np.named {
							np.name = a.suggestName(v)
						}
					}),
				ui.FormText("add-prov-name", "Name", "How sessions and the config refer to it: agent.providers.<name>",
					np.name, func(v string) { np.name, np.named = strings.TrimSpace(v), true }),
			}
			switch {
			case np.typ == "openai":
				items = append(items, ui.FormText("add-prov-base", "Base URL", "Empty is OpenAI; or OpenRouter, Ollama, LM Studio, vLLM, …",
					np.baseURL, func(v string) { np.baseURL = strings.TrimSpace(v) }))
			case np.typ == "azure":
				items = append(items, ui.FormText("add-prov-base", "Base URL", "Your resource: https://<resource>.openai.azure.com",
					np.baseURL, func(v string) { np.baseURL = strings.TrimSpace(v) }))
			}
			col := []ui.View{}
			if np.problem != "" {
				col = append(col, ui.Alert(np.problem, ui.AlertError))
			}
			col = append(col, ui.Form("add-prov-form", items...))
			if !noKeyNeeded(pc) {
				hint := "API key, stored in " + keychainName() + ", not in the config file"
				if np.typ == "google" {
					hint += ". For Vertex AI leave it empty and set the project after adding"
				}
				col = append(col, ui.Column(
					ui.Caption(hint),
					ui.TextField("add-prov-key", np.key).Password(true).Placeholder("Paste the key").
						OnChange(func(v string) { np.key = strings.TrimSpace(v) }),
				).Gap(th.Spacing.XS))
			}
			col = append(col, ui.Muted("Pick the model once it is added: the list comes from the provider."))
			return ui.Column(col...).Gap(th.Spacing.M).Padding(th.Spacing.L)
		},
		Actions: []ui.DialogAction{
			{Label: "Cancel"},
			{Label: "Add", Primary: true, OnClick: func() { a.addProviderFrom(np) }},
		},
	})
}

// addProviderFrom writes the dialog's provider, stores its key and opens
// it to choose a model.
func (a *App) addProviderFrom(np *newProvider) {
	np.problem = ""
	switch {
	case !providerNameRE.MatchString(np.name):
		np.problem = "The name takes lowercase letters, digits, - and _."
	case a.takenName(np.name, np.typ):
		np.problem = "A provider named " + np.name + " exists already."
	case np.typ == "azure" && np.baseURL == "":
		np.problem = "Azure needs your resource's URL."
	}
	if np.problem != "" {
		a.showAddProvider(np)
		return
	}
	entry := map[string]any{"type": np.typ}
	if np.baseURL != "" {
		entry["base_url"] = np.baseURL
	}
	if np.key != "" {
		if err := a.agent.keys.Set(np.name, np.key); err != nil {
			a.fail("Could not save the key", err)
			return
		}
		entry["api_key"] = "keychain:" + np.name
	}
	user, _ := a.cfg.Paths()
	if err := config.SetValue(user, []string{"agent", "providers", np.name}, entry); err != nil {
		a.fail("Could not add the provider", err)
		return
	}
	a.reloadBuffer(user)
	a.reloadConfig()
	a.refreshProviderKeys()
	a.forgetModels(np.name)
	a.compl.forgetProvider()
	a.settingsCat, a.settingsProvider = "providers", np.name
}
