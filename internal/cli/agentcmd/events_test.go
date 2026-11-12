// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package agentcmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/usehm/hm/internal/agent/agenttest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// eventTypes are the types docs/events.md names.
var eventTypes = map[string]bool{
	"session_start": true, "message_start": true, "text_delta": true, "thinking_delta": true,
	"tool_start": true, "tool_call": true, "approval_needed": true, "approval": true, "blocked": true,
	"tool_result": true, "message_end": true, "usage": true, "retry": true, "error": true,
	"compaction_start": true, "compaction_end": true, "subagent_end": true, "turn_end": true, "session_end": true, "question": true, "response": true, "warning": true,
}

// runJSON runs hm agent --output json and returns its lines, checked
// against the format: each parses, has v 1, a known type and a session,
// session_start comes first and session_end last.
func runJSON(t *testing.T, root string, args ...string) (int, []map[string]any, string) {
	t.Helper()
	old := clock
	clock = func() time.Time { return time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { clock = old })
	var out, errb bytes.Buffer
	code := Run(context.Background(), append([]string{"--cwd", root, "-p", "--output", "json"}, args...), nil, &out, &errb)
	raw := out.String()
	var lines []map[string]any
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %d does not parse: %v\n%s", len(lines)+1, err, sc.Bytes())
		}
		if m["v"] != float64(1) || !eventTypes[fmt.Sprint(m["type"])] || m["session"] == "" || m["time"] == "" {
			t.Fatalf("bad line %d: %s", len(lines)+1, sc.Bytes())
		}
		lines = append(lines, m)
	}
	if len(lines) < 2 || lines[0]["type"] != "session_start" || lines[len(lines)-1]["type"] != "session_end" {
		t.Fatalf("stream does not start and end with the session:\n%s", raw)
	}
	if got := lines[len(lines)-1]["exit_code"]; got != float64(code) {
		t.Fatalf("session_end exit_code %v, exit %d", got, code)
	}
	return code, lines, raw
}

var sessionID = regexp.MustCompile(`"session":"([^"]+)"`)

// normalize makes a stream comparable: the workspace is ROOT and session
// IDs are S1, S2… in order of appearance.
func normalize(out, root string) string {
	out = strings.ReplaceAll(out, root, "ROOT")
	ids := map[string]string{}
	return sessionID.ReplaceAllStringFunc(out, func(m string) string {
		id := sessionID.FindStringSubmatch(m)[1]
		if ids[id] == "" {
			ids[id] = fmt.Sprintf("S%d", len(ids)+1)
		}
		return `"session":"` + ids[id] + `"`
	})
}

func TestJSONGolden(t *testing.T) {
	cases := []struct {
		name   string
		script []agenttest.Reply
		args   []string
		code   int
	}{
		{"answer", []agenttest.Reply{{Text: "main does nothing"}}, []string{"what does main do?"}, exitOK},
		{"refused", []agenttest.Reply{
			{Calls: []agenttest.Call{{Name: "read", Input: `{"path":"main.go"}`}}},
			shellCall(),
			{Text: "could not run it"},
		}, []string{"read main.go, then run the sum"}, exitNeededOK},
		{"max-steps", []agenttest.Reply{{Calls: []agenttest.Call{{Name: "read", Input: `{"path":"main.go"}`}}}},
			[]string{"--max-steps", "1", "read it"}, exitAgentError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, _ := agenttest.Workspace(t, c.script, "")
			code, _, out := runJSON(t, root, append([]string{"--no-session"}, c.args...)...)
			if code != c.code {
				t.Fatalf("exit %d, want %d\n%s", code, c.code, out)
			}
			got := normalize(out, root)
			golden := filepath.Join("testdata", c.name+".jsonl")
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update)", err)
			}
			if got != string(want) {
				t.Fatalf("stream differs from %s (go test -update rewrites it):\n%s", golden, got)
			}
		})
	}
}

func TestJSONSubagentSessions(t *testing.T) {
	root, _ := agenttest.Workspace(t, []agenttest.Reply{
		{Calls: []agenttest.Call{{Name: "task", Input: `{"description":"look","prompt":"list the files"}`}}},
		{Text: "there is main.go"},
		{Text: "it found main.go"},
	}, "")
	code, lines, out := runJSON(t, root, "--no-session", "delegate")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	top := lines[0]["session"]
	var child any
	for _, l := range lines {
		if l["type"] == "subagent_end" {
			child = l["session"]
		}
	}
	if child == nil || child == top {
		t.Fatalf("sub-agent session %v, top %v", child, top)
	}
	childText := false
	for _, l := range lines {
		childText = childText || (l["session"] == child && l["type"] == "text_delta" && l["text"] == "there is main.go")
	}
	if !childText || lines[len(lines)-1]["answer"] != "it found main.go" {
		t.Fatalf("stream:\n%s", out)
	}
}
