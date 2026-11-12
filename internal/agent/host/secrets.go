package host

import (
	"os"
	"strings"

	"github.com/usehm/hm/internal/config"
)

// KnownSecrets reads the secrets of cfg and the environment, for an
// export to take out: provider and web search keys, MCP servers'
// environment and headers, and environment variables named like keys.
func KnownSecrets(cfg config.Config) []string {
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
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		if SecretName(name) {
			out = append(out, val)
		}
	}
	return out
}

// SecretName reports whether an environment variable's name says it
// holds a secret.
func SecretName(name string) bool {
	n := strings.ToUpper(name)
	for _, s := range []string{"_KEY", "_TOKEN", "_SECRET", "_PASSWORD", "_PAT"} {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	return strings.Contains(n, "API_KEY") || strings.Contains(n, "SECRET")
}
