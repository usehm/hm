// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// SchemaPath is where hm writes the config's JSON Schema for
// yaml-language-server.
func SchemaPath() string { return filepath.Join(UserDir(), "schema.json") }

// WriteSchema writes the JSON Schema when it is missing or out of date.
func WriteSchema() error {
	data := Schema()
	if old, err := os.ReadFile(SchemaPath()); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(UserDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(SchemaPath(), data, 0o644)
}

// Schema returns a JSON Schema generated from Config and Enums, so it never
// drifts from what Load accepts.
func Schema() []byte {
	s := schemaFor(reflect.TypeOf(Config{}), nil)
	s["$schema"] = "http://json-schema.org/draft-07/schema#"
	s["title"] = "hm config"
	data, _ := json.MarshalIndent(s, "", "  ")
	return append(data, '\n')
}

func schemaFor(t reflect.Type, path []string) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if words, ok := Enums[strings.Join(path, ".")]; ok {
		return map[string]any{"enum": words}
	}
	switch t {
	case reflect.TypeOf(Duration{}):
		return map[string]any{"type": "string", "pattern": `^[0-9.]+(ns|us|µs|ms|s|m|h)([0-9.]+(ns|us|µs|ms|s|m|h))*$`}
	case reflect.TypeOf(StringList{}):
		return map[string]any{"oneOf": []any{
			map[string]any{"const": "all"},
			map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}}
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			props[name] = schemaFor(f.Type, append(path, name))
		}
		return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem(), append(path, "*"))}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem(), append(path, "*"))}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	}
	return map[string]any{}
}
