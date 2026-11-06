package host

import (
	"slices"
	"testing"

	"github.com/mirzakhany/hm/internal/config"
)

func TestSecretName(t *testing.T) {
	for name, want := range map[string]bool{"ANTHROPIC_API_KEY": true, "GITHUB_TOKEN": true, "aws_secret_access_key": true,
		"DB_PASSWORD": true, "HOME": false, "PATH": false, "KEYBOARD": false} {
		if got := SecretName(name); got != want {
			t.Errorf("SecretName(%s) = %v", name, got)
		}
	}
}

func TestKnownSecrets(t *testing.T) {
	t.Setenv("HM_TEST_TOKEN", "tok-from-env")
	t.Setenv("HM_TEST_HEADER", "Bearer hdr-secret")
	cfg, ps := config.Load(config.Layer{Name: "t", Data: []byte(`
agent:
  providers:
    p: { type: openai, base_url: "http://localhost:1", api_key: "plain-key" }
tools:
  mcp:
    s: { transport: http, url: "http://localhost:2", headers: { Authorization: "${HM_TEST_HEADER}" } }
`)})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	got := KnownSecrets(cfg)
	for _, want := range []string{"plain-key", "Bearer hdr-secret", "hdr-secret", "tok-from-env"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
