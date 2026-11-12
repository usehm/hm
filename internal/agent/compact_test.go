package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/agent/tools"
)

// fakeCompactor answers with a fixed summary and records what it was asked.
type fakeCompactor struct {
	asked []SummaryRequest
	err   error
}

func (f *fakeCompactor) Summarize(_ context.Context, in SummaryRequest) (SummaryReply, error) {
	f.asked = append(f.asked, in)
	if f.err != nil {
		return SummaryReply{}, f.err
	}
	return SummaryReply{Text: "SUMMARY " + string(rune('A'+len(f.asked)-1)), Model: "small", Usage: provider.Usage{Input: 7, Cost: 0.5}}, nil
}

// Messages for the cut point tests: user text, assistant, tool results.
func ut(s string) provider.Message {
	return provider.Message{Role: provider.User, Blocks: []provider.Block{say(s)}}
}

func at(blocks ...provider.Block) provider.Message {
	return provider.Message{Role: provider.Assistant, Blocks: blocks}
}

func tr(id string) provider.Message {
	return provider.Message{Role: provider.User, Blocks: []provider.Block{{Type: provider.ToolResult, ToolID: id, ToolName: "read", Text: "ok"}}}
}

func TestCutPoint(t *testing.T) {
	big := strings.Repeat("x", 400) // about 100 tokens
	tests := []struct {
		name       string
		msgs       []provider.Message
		from, keep int
		want       int
	}{
		{"keeps the latest exchange", []provider.Message{ut(big), at(say(big)), ut(big), at(say(big))}, 0, 150, 2},
		{"snaps back from results to their call",
			[]provider.Message{ut(big), at(call("1", "read", "{}")), tr("1"), at(call("2", "read", "{}")), tr("2"), at(say("done"))}, 0, 5, 3},
		{"never starts at tool results",
			[]provider.Message{ut(big), at(say(big), call("1", "read", "{}")), tr("1"), ut(big)}, 0, 120, 1},
		{"keeps fewer when the newest are too big", []provider.Message{ut(big), at(say(big)), tr("1")}, 0, 100_000, 1},
		{"after the previous compaction", []provider.Message{ut(big), at(say(big)), ut(big), at(say(big)), ut(big)}, 2, 1, 4},
		{"nothing before the last message", []provider.Message{ut(big)}, 0, 1, -1},
		{"nothing after the previous compaction", []provider.Message{ut(big), at(say(big)), tr("1")}, 1, 1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cutPoint(tt.msgs, tt.from, tt.keep)
			if got != tt.want {
				t.Fatalf("cut at %d, want %d", got, tt.want)
			}
			if got >= 0 && !canCut(tt.msgs[got]) {
				t.Fatalf("kept part starts at %+v", tt.msgs[got])
			}
		})
	}
}

func TestForModel(t *testing.T) {
	s := NewSession("auto", "script")
	addMessages(s, ut("one"), at(say("a")), ut("two"), at(call("1", "read", "{}")), tr("1"))
	if got := s.ForModel(); len(got) != 5 {
		t.Fatalf("no compaction: %d messages", len(got))
	}

	tests := []struct {
		at       int
		n        int
		firstLen int // blocks in the first message
	}{
		{2, 3, 2}, // the user's message carries the summary too
		{3, 3, 1}, // the summary goes in its own message before the call
	}
	for _, tt := range tests {
		compactAt(s, tt.at, "S")
		got := s.ForModel()
		if len(got) != tt.n || got[0].Role != provider.User || len(got[0].Blocks) != tt.firstLen {
			t.Fatalf("at %d: %+v", tt.at, got)
		}
		if !strings.Contains(got[0].Blocks[0].Text, "<summary>\nS\n</summary>") {
			t.Fatalf("at %d: first block %q", tt.at, got[0].Blocks[0].Text)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Role == got[i-1].Role {
				t.Fatalf("at %d: two %s messages in a row", tt.at, got[i].Role)
			}
		}
	}
	if len(s.Snapshot()[2].Blocks) != 1 {
		t.Fatal("ForModel changed the stored message")
	}
}

func TestAutoCompactBeforeRequest(t *testing.T) {
	h := newAgent(t, "auto", []provider.Block{say("hi")}, []provider.Block{say("still here")})
	fc := &fakeCompactor{}
	h.agent.ContextWindow = 20
	h.agent.Compaction = &CompactConfig{Auto: true, Threshold: 0.5, KeepTokens: 1, Compactor: fc}
	h.run(t, "hello") // no context measured yet: no compaction
	if len(fc.asked) != 0 {
		t.Fatal("compacted before the first call")
	}
	h.events = nil
	h.run(t, "more") // 15 tokens used of 20, past half
	if len(fc.asked) != 1 || len(fc.asked[0].Messages) != 2 {
		t.Fatalf("asked %+v", fc.asked)
	}
	req := h.prov.requests[1]
	if len(req.Messages) != 1 || req.Messages[0].Blocks[1].Text != "more" ||
		!strings.Contains(req.Messages[0].Blocks[0].Text, "SUMMARY A") {
		t.Fatalf("sent %+v", req.Messages)
	}
	if req.System != h.prov.requests[0].System {
		t.Fatal("the system prompt changed")
	}
	if len(h.session.Snapshot()) != 4 {
		t.Fatalf("history has %d messages, want all 4", len(h.session.Snapshot()))
	}
	var compacted string
	for _, e := range h.events {
		if e.Kind == EvCompacted {
			compacted = e.Text
		}
	}
	if !strings.HasPrefix(compacted, "Compacted 15 → ") {
		t.Fatalf("event %q", compacted)
	}
	if u := h.session.UsageNow(); u.Cost != 0.5 {
		t.Fatalf("summary cost not counted: %+v", u)
	}
}

// overflowing fails with a context overflow the first n calls.
type overflowing struct {
	script
	n int
}

func (o *overflowing) Stream(ctx context.Context, req provider.Request, on func(provider.Event)) (provider.Response, error) {
	if o.n > 0 {
		o.n--
		o.requests = append(o.requests, req)
		return provider.Response{}, &provider.APIError{Kind: provider.ErrContextOverflow, Status: 400, Msg: "prompt is too long"}
	}
	return o.script.Stream(ctx, req, on)
}

func TestOverflowCompactsAndRetriesOnce(t *testing.T) {
	tests := []struct {
		name      string
		failures  int
		wantErr   bool
		wantCalls int
	}{
		{"retry succeeds", 1, false, 3},
		{"second overflow fails", 2, true, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAgent(t, "auto", []provider.Block{say("hi")})
			h.run(t, "hello")
			prov := &overflowing{script: script{turns: [][]provider.Block{{say("after")}}}, n: tt.failures}
			prov.requests = h.prov.requests
			fc := &fakeCompactor{}
			h.agent.Provider = prov
			h.agent.Compaction = &CompactConfig{Auto: true, Threshold: 0.85, KeepTokens: 1, Compactor: fc}
			err := h.agent.Run(context.Background(), h.session, "more")
			if tt.wantErr != (err != nil) || (err != nil && !errors.Is(err, provider.ErrContextOverflow)) {
				t.Fatalf("err %v", err)
			}
			if len(fc.asked) != 1 || len(prov.requests) != tt.wantCalls {
				t.Fatalf("compacted %d times, %d calls", len(fc.asked), len(prov.requests))
			}
			if !tt.wantErr && h.session.Snapshot()[len(h.session.Snapshot())-1].TextOf() != "after" {
				t.Fatal("the turn did not finish")
			}
		})
	}
}

func TestOverflowWithoutAutoCompactFails(t *testing.T) {
	h := newAgent(t, "auto", []provider.Block{say("hi")})
	h.run(t, "hello")
	fc := &fakeCompactor{}
	h.agent.Provider = &overflowing{n: 1}
	h.agent.Compaction = &CompactConfig{Auto: false, KeepTokens: 1, Compactor: fc}
	if err := h.agent.Run(context.Background(), h.session, "more"); !errors.Is(err, provider.ErrContextOverflow) || len(fc.asked) != 0 {
		t.Fatalf("err %v, compacted %d times", err, len(fc.asked))
	}
}

func TestCompactFoldsPreviousSummaryTodosAndFocus(t *testing.T) {
	s := NewSession("auto", "script")
	addMessages(s, ut("one"), at(say("a")), ut("two"), at(say("b")), ut("three"), at(say("c")))
	s.SetTodos([]tools.Todo{{Text: "fix parser", Status: "in_progress"}})
	fc := &fakeCompactor{}
	if _, err := Compact(context.Background(), s, fc, 1, "the parser bug"); err != nil {
		t.Fatal(err)
	}
	first := fc.asked[0]
	if first.Previous != "" || first.Focus != "the parser bug" || len(first.Todos) != 1 || len(first.Messages) != 5 {
		t.Fatalf("first request %+v", first)
	}
	addMessages(s, ut("four"), at(say("d")))
	c, err := Compact(context.Background(), s, fc, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	second := fc.asked[1]
	if second.Previous != "SUMMARY A" || len(second.Messages) != 2 || second.Messages[0].TextOf() != "c" {
		t.Fatalf("second request %+v", second)
	}
	if c.At != 7 || len(s.CompactionsNow()) != 2 || s.ContextUsed() != c.TokensAfter {
		t.Fatalf("compaction %+v, context %d", c, s.ContextUsed())
	}
	if got := s.ForModel(); len(got) != 2 || !strings.Contains(got[0].TextOf(), "SUMMARY B") {
		t.Fatalf("model sees %+v", got)
	}
	if _, err := Compact(context.Background(), NewSession("auto", "x"), fc, 1, ""); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("empty session: %v", err)
	}
}

func TestCompactionSavedAndReloaded(t *testing.T) {
	s := NewSession("auto", "script")
	addMessages(s, ut("one"), at(say("a")), ut("two"), at(say("b")))
	if _, err := Compact(context.Background(), s, &fakeCompactor{}, 1, ""); err != nil {
		t.Fatal(err)
	}
	st := NewStore(t.TempDir())
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Snapshot()) != 4 || len(got.CompactionsNow()) != 1 || got.CompactionsNow()[0].Summary != "SUMMARY A" {
		t.Fatalf("loaded %+v", got)
	}
	if a, b := got.ForModel(), s.ForModel(); len(a) != len(b) || a[0].TextOf() != b[0].TextOf() {
		t.Fatalf("model view changed: %+v vs %+v", a, b)
	}
}

func TestModelCompactorPrompt(t *testing.T) {
	prov := &script{turns: [][]provider.Block{{say("  the summary  ")}}}
	m := ModelCompactor{Provider: prov, Model: "small"}
	long := strings.Repeat("y", maxResultChars+100)
	reply, err := m.Summarize(context.Background(), SummaryRequest{
		Previous: "old summary",
		Messages: []provider.Message{ut("fix it"), at(call("1", "read", `{"path":"a.go"}`)),
			{Role: provider.User, Blocks: []provider.Block{{Type: provider.ToolResult, ToolID: "1", ToolName: "read", Text: long}}}},
		Todos: []tools.Todo{{Text: "write tests", Status: "pending"}},
		Focus: "the parser bug",
	})
	if err != nil || reply.Text != "the summary" || reply.Model != "small" {
		t.Fatalf("reply %+v, %v", reply, err)
	}
	req := prov.requests[0]
	text := req.Messages[0].TextOf()
	for _, want := range []string{"old summary", "[user]\nfix it", `[assistant called read]` + "\n" + `{"path":"a.go"}`,
		"more characters]", "- [pending] write tests", "focus on: the parser bug"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if req.Model != "small" || !strings.Contains(req.System, "## Next step") || len(req.Tools) != 0 {
		t.Fatalf("request %+v", req)
	}
}

func TestSmallWindowKeepsLess(t *testing.T) {
	big := strings.Repeat("x", 400)
	h := newAgent(t, "auto")
	addMessages(h.session, ut(big), at(say(big)), ut(big), at(say(big)), ut(big), at(say(big)))
	fc := &fakeCompactor{}
	h.agent.ContextWindow = 800 // keeps at most 200 tokens, not 20k
	h.agent.Compaction = &CompactConfig{KeepTokens: 20000, Compactor: fc}
	if err := h.agent.Compact(context.Background(), h.session, ""); err != nil {
		t.Fatal(err)
	}
	if c := h.session.CompactionsNow()[0]; c.At != 4 {
		t.Fatalf("kept from %d, want 4", c.At)
	}
}
