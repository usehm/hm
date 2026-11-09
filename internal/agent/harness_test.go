package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

func TestStoreMigratesWorkspaceSessions(t *testing.T) {
	state, root := t.TempDir(), "/work/proj"
	sum := sha256.Sum256([]byte(root))
	old := filepath.Join(state, "sessions", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	s := NewSession("auto", "p")
	s.Title = "old one"
	data, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(old, s.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewStore(state)
	if err := st.Migrate(root); err != nil {
		t.Fatal(err)
	}
	list := st.List()
	if len(list) != 1 || list[0].Root != root || list[0].Title != "old one" {
		t.Fatalf("list %+v", list)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the old folder should be gone")
	}
	if err := st.Delete(s.ID); err != nil || len(st.List()) != 0 {
		t.Fatalf("delete: %v %+v", err, st.List())
	}
}

func TestRepairAnswersDanglingCalls(t *testing.T) {
	s := NewSession("auto", "p")
	s.append(provider.Message{Role: provider.User, Blocks: []provider.Block{say("go")}})
	s.append(provider.Message{Role: provider.Assistant, Blocks: []provider.Block{call("c1", "shell", `{}`)}})
	if !s.Repair() {
		t.Fatal("repair should answer the call")
	}
	last := s.Snapshot()[len(s.Snapshot())-1]
	if last.Role != provider.User || last.Blocks[0].ToolID != "c1" || !last.Blocks[0].IsError {
		t.Fatalf("last %+v", last)
	}
	if s.Repair() {
		t.Fatal("a repaired session needs nothing more")
	}
	if len(s.TimesNow()) != len(s.Snapshot()) {
		t.Fatal("every message has a time")
	}
	// The next message joins the tool results rather than following them.
	s.addUserText("continue")
	if n := len(s.Snapshot()); n != 3 || s.Snapshot()[2].Blocks[1].Text != "continue" {
		t.Fatalf("messages %+v", s.Snapshot())
	}
}

func TestProjectsRoundTripAndPrompt(t *testing.T) {
	state := t.TempDir()
	ps, err := LoadProjects(state)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ps.Put(Project{Title: " Billing ", Description: "The billing service", Context: "Use cents, never floats."})
	if err != nil || p.ID == "" || p.Title != "Billing" {
		t.Fatalf("put %+v %v", p, err)
	}
	again, err := LoadProjects(state)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := again.Get(p.ID)
	if !ok || got.Context != "Use cents, never floats." {
		t.Fatalf("get %+v %v", got, ok)
	}
	sys := System(Mode{Name: "auto"}, "/r", now(), &got, nil, "")
	if !strings.Contains(sys, "# Project context") || !strings.Contains(sys, "Use cents, never floats.") {
		t.Fatalf("system prompt lacks the project:\n%s", sys)
	}
	if strings.Contains(System(Mode{Name: "auto"}, "/r", now(), nil, nil, ""), "Project context") {
		t.Fatal("no project, no project context")
	}
	if err := again.Delete(p.ID); err != nil || len(again.List()) != 0 {
		t.Fatalf("delete %v %+v", err, again.List())
	}
}

func TestSteeringJoinsAfterToolResults(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "read", `{"path":"a.go"}`)},
		[]provider.Block{say("got it")},
		[]provider.Block{say("and the second note")},
	)
	queue := []string{"also check b.go"}
	saves := 0
	h.agent.Hooks.Steer = func() []Input { q := queue; queue = nil; return Texts(q...) }
	h.agent.Hooks.Persist = func() { saves++ }
	h.run(t, "read a")
	// The note was waiting before the call started, so the call was
	// skipped and the note joined its result.
	second := h.prov.requests[1].Messages
	last := second[len(second)-1]
	if last.Blocks[len(last.Blocks)-1].Text != "also check b.go" {
		t.Fatalf("note not after the tool results: %+v", last)
	}
	if r := last.Blocks[0]; r.Type != provider.ToolResult || !r.IsError || r.Text != skippedText {
		t.Fatalf("call not skipped: %+v", r)
	}
	if len(h.prov.requests) != 2 {
		t.Fatalf("requests %d", len(h.prov.requests))
	}
	if saves == 0 {
		t.Fatal("Persist was never called")
	}

	// A note arriving as the model ends its turn starts another step.
	queue = []string{"one more thing"}
	h.prov.turns = [][]provider.Block{{say("ok")}, {say("done too")}}
	h.run(t, "thanks")
	if got := h.session.Snapshot()[len(h.session.Snapshot())-1].TextOf(); got != "done too" {
		t.Fatalf("last %q", got)
	}
}

// A steering message sent during the first of three calls lets that call
// finish and skips the other two; a follow-up waits for the turn to end.
func TestSteerSkipsRestFollowUpWaits(t *testing.T) {
	tests := []struct {
		name     string
		followUp bool
		real     int // calls that ran
		requests int // model calls in the turn
		joinsAt  int // request whose last message ends with the text
	}{
		{name: "steer", real: 1, requests: 2, joinsAt: 1},
		{name: "follow-up", followUp: true, real: 3, requests: 3, joinsAt: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAgent(t, "auto",
				[]provider.Block{
					call("c1", "read", `{"path":"a.go"}`),
					call("c2", "read", `{"path":"a.go"}`),
					call("c3", "read", `{"path":"a.go"}`),
				},
				[]provider.Block{say("done")},
				[]provider.Block{say("done after the note")},
			)
			var steer, follow []string
			send := func(m string) {
				if tt.followUp {
					follow = append(follow, m)
				} else {
					steer = append(steer, m)
				}
			}
			event := h.agent.Hooks.Event
			h.agent.Hooks.Event = func(e Event) {
				if e.Kind == EvToolResult && e.CallID == "c1" {
					send("stop, look at b.go")
				}
				if event != nil {
					event(e)
				}
			}
			h.agent.Hooks.Steer = func() []Input { q := steer; steer = nil; return Texts(q...) }
			h.agent.Hooks.FollowUp = func() []Input { q := follow; follow = nil; return Texts(q...) }
			h.run(t, "read a three times")

			res := h.results()
			if len(res) != 3 {
				t.Fatalf("results %d, want one per call", len(res))
			}
			real := 0
			for _, r := range res {
				if r.Text != skippedText {
					real++
				} else if !r.IsError {
					t.Fatalf("a skipped result should be an error: %+v", r)
				}
			}
			if real != tt.real {
				t.Fatalf("%d calls ran, want %d", real, tt.real)
			}
			if len(h.prov.requests) != tt.requests {
				t.Fatalf("requests %d, want %d", len(h.prov.requests), tt.requests)
			}
			msgs := h.prov.requests[tt.joinsAt].Messages
			last := msgs[len(msgs)-1]
			if got := last.Blocks[len(last.Blocks)-1].Text; got != "stop, look at b.go" {
				t.Fatalf("request %d ends with %q", tt.joinsAt, got)
			}
			if tt.followUp {
				// The follow-up came after the model's answer, alone.
				if prev := msgs[len(msgs)-2]; prev.Role != provider.Assistant || prev.TextOf() != "done" {
					t.Fatalf("follow-up did not wait for the turn to end: %+v", prev)
				}
			}
		})
	}
}

func TestBusSendWaitAndPeers(t *testing.T) {
	var woke []string
	b := NewBus(func(id string) { woke = append(woke, id) })
	b.Register("p", "parent task", "")
	b.Register("c1", "finder", "p")
	b.Register("c2", "fixer", "p")
	if err := b.Send("c1", "parent", "found it", false); err != nil {
		t.Fatal(err)
	}
	if err := b.Send("c2", "parent", "report", true); err != nil {
		t.Fatal(err)
	}
	if err := b.Send("p", "fixer", "go", false); err != nil {
		t.Fatal(err)
	}
	if err := b.Send("c1", "nobody", "x", false); err == nil {
		t.Fatal("unknown names should fail")
	}
	if len(woke) != 3 || woke[0] != "p" || woke[2] != "c2" {
		t.Fatalf("notified %v", woke)
	}
	// Take takes one kind and leaves the other.
	if m := b.Take("p", false); len(m) != 1 || m[0].FromName != "finder" {
		t.Fatalf("take steering %+v", m)
	}
	if m := b.Take("p", true); len(m) != 1 || m[0].Text != "report" || !m[0].FollowUp {
		t.Fatalf("take follow-ups %+v", m)
	}
	m, ok := b.Wait(context.Background(), "c2", "parent task", time.Second)
	if !ok || m.Text != "go" {
		t.Fatalf("wait %+v %v", m, ok)
	}
	if _, ok := b.Wait(context.Background(), "c2", "", 10*time.Millisecond); ok {
		t.Fatal("an empty mailbox should time out")
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = b.Send("c2", "c1", "ping", true)
	}()
	if m, ok := b.Wait(context.Background(), "c1", "", time.Second); !ok || m.Text != "ping" {
		t.Fatalf("wait for a later message %+v %v", m, ok)
	}
	peers := b.Peers("c1")
	if len(peers) != 2 || peers[0].Role != "sibling" || peers[1].Role != "parent" {
		t.Fatalf("peers %+v", peers)
	}
}
