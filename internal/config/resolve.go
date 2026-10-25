package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Keychain looks up a secret stored under name. The app sets it to the OS
// keychain; until then keychain: values fail to resolve.
var Keychain func(name string) (string, error)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Resolve expands a config value at use time: "keychain:<name>" reads the
// keychain, and ${VAR} references read the environment. Secrets never live
// in the YAML itself.
func Resolve(v string) (string, error) {
	if name, ok := strings.CutPrefix(v, "keychain:"); ok {
		if Keychain == nil {
			return "", fmt.Errorf("no keychain available for %q", name)
		}
		s, err := Keychain(name)
		if err != nil {
			return "", fmt.Errorf("keychain %q: %w", name, err)
		}
		return s, nil
	}
	var missing []string
	out := envRef.ReplaceAllStringFunc(v, func(m string) string {
		name := m[2 : len(m)-1]
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
		}
		return val
	})
	if len(missing) > 0 {
		return out, fmt.Errorf("environment variable %s is not set", strings.Join(missing, ", "))
	}
	return out, nil
}
