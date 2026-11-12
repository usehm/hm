// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package config

import "testing"

func TestParseWhen(t *testing.T) {
	ctx := map[string]bool{"editorFocus": true, "vimInsert": false, "terminalFocus": false}
	for s, want := range map[string]bool{
		"":                                    true,
		"editorFocus":                         true,
		"!editorFocus":                        false,
		"editorFocus && !vimInsert":           true,
		"terminalFocus || editorFocus":        true,
		"!(editorFocus || terminalFocus)":     false,
		"terminalFocus && x || editorFocus":   true,
		"terminalFocus && (x || editorFocus)": false,
	} {
		w, err := ParseWhen(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if got := w.Eval(ctx); got != want {
			t.Errorf("%q = %v, want %v", s, got, want)
		}
	}
	for _, bad := range []string{"a &&", "(a", "a b", "&& a", "a == b"} {
		if _, err := ParseWhen(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestKeybindingsValidate(t *testing.T) {
	_, ps := Load(Layer{Name: "u", Data: []byte(`keybindings:
  - { key: "cmd+k cmd+s", command: file.saveAll }
  - { key: "alt+mouse4", command: nav.back, when: "editorFocus &&" }
keys:
  file.save: "cmd+k cmd+k cmd+k"
`)})
	if len(ps) != 2 {
		t.Fatalf("problems %v, want the bad condition and the three-chord key", ps)
	}
}
