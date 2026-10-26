package app

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/config"
)

// The OS keychain runs a process per read, so Body must never read it:
// the agent header and settings page show a key status read off the UI
// goroutine.
func TestFramesDoNotReadKeychain(t *testing.T) {
	root := project(t)
	writeFile(t, filepath.Join(root, ".hm", "config.yaml"), `
agent:
  default_provider: test
  providers:
    test: { type: openai, base_url: "https://example.invalid/v1", api_key: "keychain:test", model: m }
`)
	h := newHarness(t, root, false)
	var reads atomic.Int32
	read := config.Keychain
	config.Keychain = func(name string) (string, error) {
		reads.Add(1)
		return read(name)
	}
	t.Cleanup(func() { config.Keychain = read })

	if err := h.app.agent.keys.Set("test", "sk-test"); err != nil {
		t.Fatal(err)
	}
	h.app.refreshProviderKeys()
	h.until("the key status", 2*time.Second, func() bool { return h.app.hasProviderKey("test") })

	reads.Store(0)
	h.app.agentOpen = true
	h.app.openSettings()
	for range 20 {
		h.frame()
	}
	if n := reads.Load(); n != 0 {
		t.Fatalf("frames read the keychain %d times", n)
	}
}
