// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// A fresh install lists no providers: the built-in ones wait for a key.
func TestProvidersStartEmpty(t *testing.T) {
	h := newHarness(t, project(t), false)
	if got := h.app.listedProviders(); len(got) != 0 {
		t.Fatalf("listed %v", got)
	}
	h.app.openSettingsAt("providers")
	h.frame() // the empty list draws
}

// The add dialog's provider lands in the user config with its key in the
// keychain, connects, and opens to pick a model.
func TestAddProviderAndConnect(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{nil}}
	h, _ := agentProject(t, "openai", model)
	base := h.app.cfg.Current().Agent.Providers["test"].BaseURL
	h.app.addProviderFrom(&newProvider{typ: "openai", name: "mine", baseURL: base, key: "sk-mine"})
	if h.app.settingsProvider != "mine" || h.app.settingsCat != "providers" {
		t.Fatalf("should open the new provider, at %q %q", h.app.settingsCat, h.app.settingsProvider)
	}
	h.app.openSettings() // the detail page draws while it connects
	user, _ := h.app.cfg.Paths()
	data, _ := os.ReadFile(user)
	if !strings.Contains(string(data), "keychain:mine") || strings.Contains(string(data), "sk-mine") {
		t.Fatalf("user config: %s", data)
	}
	if v, _ := h.app.agent.keys.Get("mine"); v != "sk-mine" {
		t.Fatalf("keychain %q", v)
	}
	h.until("connected", 10*time.Second, func() bool {
		h.frame()
		return h.app.providerStatus("mine").label == "Connected"
	})
	if st := h.app.providerStatus("mine"); len(st.models) != 2 {
		t.Fatalf("models %v", st.models)
	}
	if !slices.Contains(h.app.listedProviders(), "mine") {
		t.Fatal("the new provider should be listed")
	}
	h.app.settingsProvider = ""
	h.frame() // the list draws its cards
}

func TestAddProviderRejectsTakenName(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{nil}}
	h, _ := agentProject(t, "openai", model)
	np := &newProvider{typ: "openai", name: "test", named: true}
	h.app.addProviderFrom(np)
	if np.problem == "" {
		t.Fatal("a taken name should be refused")
	}
	// A built-in provider not set up yet is free for its own type.
	if h.app.takenName("anthropic", "anthropic") || !h.app.takenName("anthropic", "openai") {
		t.Fatal("anthropic's name")
	}
	if got := h.app.suggestName("openai"); got != "openai" {
		t.Fatalf("suggested %q", got)
	}
}

// Disabled, a provider stays listed but leaves the pickers.
func TestDisableAndDeleteProvider(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{nil}}
	h, _ := agentProject(t, "openai", model)
	h.app.addProviderFrom(&newProvider{typ: "groq", name: "fast", key: "gsk"})
	h.until("listed", 10*time.Second, func() bool { return h.app.providerReady("fast") })
	h.app.setUserSetting([]string{"agent", "providers", "fast", "disabled"}, true)
	if slices.Contains(h.app.providerNames(), "fast") || !slices.Contains(h.app.listedProviders(), "fast") {
		t.Fatal("disabled: off the pickers, on the page")
	}
	if st := h.app.providerStatus("fast"); st.label != "Disabled" {
		t.Fatalf("status %q", st.label)
	}
	h.app.deleteProvider("fast")
	if _, ok := h.app.cfg.Current().Agent.Providers["fast"]; ok {
		t.Fatal("deleted provider is still configured")
	}
	if _, err := h.app.agent.keys.Get("fast"); err == nil {
		t.Fatal("its key should be gone")
	}
}
