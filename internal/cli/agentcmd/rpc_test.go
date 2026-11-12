package agentcmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usehm/hm/internal/agent/agenttest"
)

// rpcHarness drives hm agent --rpc in-process: commands go in through a
// pipe, and every line that comes out is checked and kept.
type rpcHarness struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan map[string]any
	seen  []map[string]any
	// claimed marks the lines of seen an until returned, so each wait
	// takes the first line it has not had yet, read or still to come.
	claimed map[int]bool
	code    chan int
	raw     *bytes.Buffer
}

func startRPC(t *testing.T, root string, args ...string) *rpcHarness {
	t.Helper()
	old := clock
	clock = func() time.Time { return time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { clock = old })
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &rpcHarness{t: t, in: inW, claimed: map[int]bool{}, lines: make(chan map[string]any, 1000), code: make(chan int, 1), raw: &bytes.Buffer{}}
	var stderr bytes.Buffer
	go func() {
		h.code <- Run(context.Background(), append([]string{"--cwd", root, "--rpc"}, args...), inR, outW, &stderr)
		outW.Close()
	}()
	go func() {
		defer close(h.lines)
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			h.raw.Write(append(sc.Bytes(), '\n'))
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("line does not parse: %v\n%s", err, sc.Bytes())
				continue
			}
			if m["v"] != float64(1) || !eventTypes[fmt.Sprint(m["type"])] {
				t.Errorf("bad line: %s", sc.Bytes())
			}
			h.lines <- m
		}
	}()
	t.Cleanup(func() { inW.Close() })
	h.until("session_start", func(m map[string]any) bool { return m["type"] == "session_start" })
	return h
}

func (h *rpcHarness) send(cmd map[string]any) {
	h.t.Helper()
	data, _ := json.Marshal(cmd)
	if _, err := h.in.Write(append(data, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *rpcHarness) sendRaw(s string) {
	h.t.Helper()
	if _, err := io.WriteString(h.in, s+"\n"); err != nil {
		h.t.Fatal(err)
	}
}

// until returns the first line that matches and no earlier until took:
// one already read, or else the next to come. Responses can land among a
// turn's events in any order, so waits must not depend on it.
func (h *rpcHarness) until(what string, match func(map[string]any) bool) map[string]any {
	h.t.Helper()
	for i, m := range h.seen {
		if !h.claimed[i] && match(m) {
			h.claimed[i] = true
			return m
		}
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-h.lines:
			if !ok {
				h.t.Fatalf("output ended waiting for %s:\n%s", what, h.raw)
			}
			h.seen = append(h.seen, m)
			if match(m) {
				h.claimed[len(h.seen)-1] = true
				return m
			}
		case <-deadline:
			h.t.Fatalf("no %s:\n%s", what, h.raw)
		}
	}
}

func is(typ string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == typ }
}

// response waits for the response to command id.
func (h *rpcHarness) response(id string) map[string]any {
	h.t.Helper()
	return h.until("response "+id, func(m map[string]any) bool { return m["type"] == "response" && m["id"] == id })
}

// ok waits for the response to id and fails unless it succeeded.
func (h *rpcHarness) ok(id string) map[string]any {
	h.t.Helper()
	r := h.response(id)
	if r["ok"] != true {
		h.t.Fatalf("command %s failed: %v", id, r["error"])
	}
	return r
}

// end closes stdin and waits for session_end and the exit code.
func (h *rpcHarness) end() (map[string]any, int) {
	h.t.Helper()
	h.in.Close()
	last := h.until("session_end", is("session_end"))
	for range h.lines {
	}
	return last, <-h.code
}

func TestRPCApprovalRoundTrip(t *testing.T) {
	root, model := agenttest.Workspace(t, []agenttest.Reply{shellCall(), {Text: "it printed 3345"}}, "")
	h := startRPC(t, root, "--no-session")
	h.send(map[string]any{"id": "1", "type": "prompt", "text": "run the sum"})
	h.ok("1")
	need := h.until("approval_needed", is("approval_needed"))
	if need["command"] != "echo $((1000+2345))" {
		t.Fatalf("approval_needed %v", need)
	}
	h.send(map[string]any{"id": "2", "type": "get_state"})
	state := h.ok("2")["data"].(map[string]any)
	if state["running"] != true || fmt.Sprint(state["pending_approvals"]) != fmt.Sprintf("[%s]", need["call_id"]) {
		t.Fatalf("state while waiting %v", state)
	}
	h.send(map[string]any{"id": "3", "type": "approve", "call_id": need["call_id"], "allow": true})
	h.ok("3")
	res := h.until("tool_result", is("tool_result"))
	if !strings.Contains(fmt.Sprint(res["text"]), "3345") {
		t.Fatalf("result %v", res)
	}
	if end := h.until("turn_end", is("turn_end")); end["error"] != nil {
		t.Fatalf("turn_end %v", end)
	}
	h.send(map[string]any{"id": "4", "type": "approve", "call_id": need["call_id"], "allow": true})
	if r := h.response("4"); r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "no approval is waiting") {
		t.Fatalf("second approve %v", r)
	}
	last, code := h.end()
	if code != 0 || last["answer"] != "it printed 3345" || !model.Sent("3345") {
		t.Fatalf("exit %d, end %v", code, last)
	}

	// The events, without responses, whose place among them depends on
	// timing, match the golden transcript.
	var events bytes.Buffer
	for _, l := range strings.SplitAfter(h.raw.String(), "\n") {
		if l != "" && !strings.Contains(l, `"type":"response"`) {
			events.WriteString(l)
		}
	}
	got := normalize(events.String(), root)
	golden := filepath.Join("testdata", "rpc.jsonl")
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
		t.Fatalf("events differ from %s (go test -update rewrites it):\n%s", golden, got)
	}
}

func TestRPCSteerAndAbort(t *testing.T) {
	root, model := agenttest.Workspace(t, []agenttest.Reply{
		{Calls: []agenttest.Call{{Name: "shell", Input: `{"command":"echo first"}`}, {Name: "shell", Input: `{"command":"echo second"}`}}},
		{Text: "looked elsewhere"},
		shellCall(),
	}, "")
	h := startRPC(t, root, "--no-session")
	h.send(map[string]any{"id": "p1", "type": "prompt", "text": "run both"})
	h.ok("p1")
	need := h.until("approval_needed", is("approval_needed"))
	h.send(map[string]any{"id": "busy", "type": "prompt", "text": "again"})
	if r := h.response("busy"); r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "a turn is running") {
		t.Fatalf("prompt while busy %v", r)
	}
	h.send(map[string]any{"id": "s", "type": "steer", "text": "stop, look elsewhere"})
	h.ok("s")
	h.send(map[string]any{"id": "a", "type": "approve", "call_id": need["call_id"], "allow": true})
	h.ok("a")
	skipped := h.until("the second call skipped", func(m map[string]any) bool {
		return m["type"] == "tool_result" && strings.Contains(fmt.Sprint(m["text"]), "Skipped")
	})
	if skipped["call_id"] == need["call_id"] {
		t.Fatalf("the approved call was skipped")
	}
	h.until("turn_end", is("turn_end"))
	if !model.Sent("stop, look elsewhere") {
		t.Fatal("the steering message did not reach the model")
	}

	h.send(map[string]any{"id": "p2", "type": "prompt", "text": "run the sum"})
	h.ok("p2")
	h.until("approval_needed", is("approval_needed"))
	h.send(map[string]any{"id": "x", "type": "abort"})
	h.ok("x")
	if end := h.until("turn_end", is("turn_end")); !strings.Contains(fmt.Sprint(end["error"]), "canceled") {
		t.Fatalf("aborted turn_end %v", end)
	}
	if model.Sent("3345") {
		t.Fatal("the aborted call ran")
	}
	h.send(map[string]any{"id": "p3", "type": "prompt", "text": "still there?"})
	h.ok("p3")
	if end := h.until("turn_end", is("turn_end")); end["error"] != nil {
		t.Fatalf("prompt after abort %v", end)
	}
	if _, code := h.end(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestRPCQuestion(t *testing.T) {
	root, model := agenttest.Workspace(t, []agenttest.Reply{
		{Calls: []agenttest.Call{{Name: "ask_user", Input: `{"question":"which file?"}`}}},
		{Text: "thanks"},
	}, "")
	h := startRPC(t, root, "--no-session")
	h.send(map[string]any{"id": "1", "type": "prompt", "text": "edit something"})
	q := h.until("question", is("question"))
	if q["text"] != "which file?" || q["call_id"] == "" {
		t.Fatalf("question %v", q)
	}
	h.send(map[string]any{"id": "2", "type": "answer", "call_id": q["call_id"], "text": "the one named kumquat.go"})
	h.ok("2")
	h.until("turn_end", is("turn_end"))
	if !model.Sent("kumquat.go") {
		t.Fatal("the answer did not reach the model")
	}
	h.end()
}

func TestRPCCommands(t *testing.T) {
	root, model := agenttest.Workspace(t, []agenttest.Reply{{Text: "first answer"}, {Text: "second answer"}}, "")
	h := startRPC(t, root)
	first := h.seen[0]["session"]

	h.send(map[string]any{"id": "m", "type": "set_mode", "mode": "careful"})
	h.ok("m")
	h.send(map[string]any{"id": "mm", "type": "set_model", "model": "other-model"})
	h.ok("mm")
	h.send(map[string]any{"id": "ef", "type": "set_model", "effort": "low"})
	h.ok("ef")
	h.send(map[string]any{"id": "badef", "type": "set_model", "effort": "lots"})
	if h.response("badef")["ok"] != false {
		t.Fatal("unknown effort accepted")
	}
	h.send(map[string]any{"id": "bad", "type": "set_mode", "mode": "nope"})
	if h.response("bad")["ok"] != false {
		t.Fatal("unknown mode accepted")
	}
	key := "sk-ant-api03-" + strings.Repeat("Q", 40)
	h.send(map[string]any{"id": "p", "type": "prompt", "text": "remember " + key})
	h.until("turn_end", is("turn_end"))
	if !model.Sent(`"other-model"`) || !model.Sent(`"effort":"low"`) {
		t.Fatal("set_model did not apply")
	}
	h.send(map[string]any{"id": "st", "type": "get_state"})
	s := h.ok("st")["data"].(map[string]any)["session"].(map[string]any)
	if s["mode"] != "careful" || s["model"] != "other-model" || s["effort"] != "low" || s["saved"] != true {
		t.Fatalf("state %v", s)
	}

	// compact answers once it is done, after its compaction lines.
	h.send(map[string]any{"id": "c", "type": "compact"})
	h.until("compaction_end", is("compaction_end"))
	h.ok("c")

	out := filepath.Join(t.TempDir(), "chat.md")
	h.send(map[string]any{"id": "e", "type": "export", "format": "md", "path": out})
	data := h.ok("e")["data"].(map[string]any)
	file, err := os.ReadFile(out)
	if err != nil || data["path"] != out {
		t.Fatalf("export %v: %v", data, err)
	}
	if strings.Contains(string(file), key) || !strings.Contains(string(file), "remember") || len(data["redacted"].([]any)) == 0 {
		t.Fatalf("export not redacted: %v\n%s", data, file)
	}

	h.send(map[string]any{"id": "f", "type": "fork"})
	fork := h.ok("f")["data"].(map[string]any)
	if fork["id"] == first || fork["id"] == "" {
		t.Fatalf("fork %v", fork)
	}
	if fork["running"] != false {
		t.Fatalf("fork says it runs: %v", fork)
	}
	started := false // written before the response, so already read
	for _, m := range h.seen {
		started = started || (m["type"] == "session_start" && m["session"] == fork["id"])
	}
	if !started {
		t.Fatal("no session_start for the fork")
	}
	h.send(map[string]any{"id": "p2", "type": "prompt", "text": "go on"})
	h.until("turn_end", is("turn_end"))
	if !strings.Contains(model.Last(), "remember") {
		t.Fatal("the fork lost the history")
	}
	h.send(map[string]any{"id": "l", "type": "list_sessions"})
	ids := map[any]bool{}
	for _, s := range h.ok("l")["data"].(map[string]any)["sessions"].([]any) {
		ids[s.(map[string]any)["id"]] = true
	}
	if !ids[first] || !ids[fork["id"]] {
		t.Fatalf("list_sessions %v, want %v and %v", ids, first, fork["id"])
	}

	h.sendRaw("not json")
	if r := h.until("error response", func(m map[string]any) bool { return m["type"] == "response" && m["id"] == nil }); r["ok"] != false || r["command"] != "" {
		t.Fatalf("bad line %v", r)
	}
	h.sendRaw(`{"id":7,"type":"dance"}`)
	if r := h.until("unknown response", func(m map[string]any) bool { return m["type"] == "response" && m["id"] == float64(7) }); r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), `unknown command "dance"`) {
		t.Fatalf("unknown command %v", r)
	}
	h.send(map[string]any{"id": "ans", "type": "answer", "call_id": "nope", "text": "x"})
	if h.response("ans")["ok"] != false {
		t.Fatal("answer to nothing accepted")
	}
	if last, code := h.end(); code != 0 || last["session"] != fork["id"] || last["answer"] != "done" {
		t.Fatalf("exit %d, end %v", code, last)
	}
}

func TestRPCEndOfInputWaitsForTheTurn(t *testing.T) {
	root, _ := agenttest.Workspace(t, []agenttest.Reply{{Text: "finished anyway"}}, "")
	h := startRPC(t, root, "--no-session")
	h.send(map[string]any{"id": "1", "type": "prompt", "text": "hi"})
	last, code := h.end()
	if code != 0 || last["answer"] != "finished anyway" {
		t.Fatalf("exit %d, end %v", code, last)
	}
	sawTurn := false
	for _, m := range h.seen {
		sawTurn = sawTurn || m["type"] == "turn_end"
	}
	if !sawTurn {
		t.Fatal("stdin's end did not wait for the turn")
	}
}

func TestRPCFlags(t *testing.T) {
	root, _ := agenttest.Workspace(t, nil, "")
	for _, args := range [][]string{
		{"--rpc", "-p", "hi"},
		{"--rpc", "a prompt"},
		{"--rpc", "--approve=never"},
		{"-p", "--approve=ask", "hi"},
	} {
		var out, errb bytes.Buffer
		if code := Run(context.Background(), append([]string{"--cwd", root}, args...), nil, &out, &errb); code != exitUsage || out.Len() > 0 {
			t.Errorf("%v: exit %d, stdout %q", args, code, out.String())
		}
	}
}

func TestRPCImages(t *testing.T) {
	root, model := agenttest.Workspace(t, []agenttest.Reply{
		{Calls: []agenttest.Call{{Name: "read", Input: `{"path":"shot.png"}`}}},
		{Text: "two blank pictures"},
	}, "")
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 6, 3))); err != nil {
		t.Fatal(err)
	}
	pic := b.Bytes()
	agenttest.WriteFile(t, filepath.Join(root, "shot.png"), string(pic))
	h := startRPC(t, root, "--no-session", "--approve", "all")

	h.send(map[string]any{"id": "bad", "type": "prompt", "text": "look", "images": []any{map[string]any{"data": []byte("not a picture")}}})
	if r := h.response("bad"); r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "image 1") {
		t.Fatalf("a bad image was taken: %v", r)
	}

	h.send(map[string]any{"id": "p", "type": "prompt", "text": "what are these?", "images": []any{map[string]any{"data": pic}}})
	h.ok("p")
	b64 := base64.StdEncoding.EncodeToString(pic)
	user := h.until("the user's message", func(m map[string]any) bool { return m["type"] == "message_end" && m["role"] == "user" })
	if got := fmt.Sprint(user["images"]); !strings.Contains(got, b64) || !strings.Contains(got, "image/png") {
		t.Fatalf("message_end images %s", got)
	}
	res := h.until("tool_result", is("tool_result"))
	if got := fmt.Sprint(res["images"]); !strings.Contains(got, b64) {
		t.Fatalf("tool_result images %s", got)
	}
	h.until("turn_end", is("turn_end"))
	if !model.Sent(b64) {
		t.Fatal("the picture did not reach the model")
	}
	if _, code := h.end(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
