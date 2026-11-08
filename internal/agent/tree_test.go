package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// texts is each message's text, for comparing paths briefly.
func texts(msgs []provider.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.TextOf()
	}
	return out
}

// reload saves s and reads it back.
func reload(t *testing.T, st *Store, s *Session) *Session {
	t.Helper()
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// twoTurns is a session of u1 a1 u2 a2, with the entry IDs.
func twoTurns() (*Session, []string) {
	s := NewSession("auto", "p")
	addMessages(s, ut("u1"), at(say("a1")), ut("u2"), at(say("a2")))
	return s, s.MessageIDs()
}

func TestMigrateV1(t *testing.T) {
	tests := []struct {
		id        string
		texts     []string
		comps     int
		forModel  int
		todos     int
		context   int64
		cost      float64
		openCalls bool
	}{
		{"20261020-100000-aaaaaaaaaaaa", []string{"list the Go files", "Looking.", "", "There are two: main.go and util.go."}, 0, 4, 0, 1280, 0.0042, false},
		{"20261021-090000-bbbbbbbbbbbb", []string{"fix the parser", "Reading parser.go.", "also add tests", "Added parser_test.go.", "run them", "All pass."}, 1, 4, 2, 9000, 1.25, false},
		{"20261022-080000-cccccccccccc", []string{"check the build", ""}, 0, 2, 0, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			state := t.TempDir()
			st := NewStore(state)
			data, err := os.ReadFile(filepath.Join("testdata", "v1", tt.id+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var v1 sessionV1
			if err := json.Unmarshal(data, &v1); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(st.dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(st.v1File(tt.id), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if l := st.List(); len(l) != 1 || l[0].ID != tt.id || l[0].Updated != v1.Updated {
				t.Fatalf("listed before opening: %+v", l)
			}

			s, err := st.Load(tt.id)
			if err != nil {
				t.Fatal(err)
			}
			for _, got := range []*Session{s, reload(t, NewStore(state), s)} {
				if g := texts(got.Snapshot()); !reflect.DeepEqual(g, tt.texts) {
					t.Fatalf("messages %q", g)
				}
				if !reflect.DeepEqual(got.Snapshot(), v1.Messages) {
					t.Fatal("messages changed in migration")
				}
				if times := got.TimesNow(); len(v1.Times) > 0 && !reflect.DeepEqual(times, v1.Times) {
					t.Fatalf("times %v", times)
				}
				if n := len(got.CompactionsNow()); n != tt.comps {
					t.Fatalf("%d compactions", n)
				}
				if n := len(got.ForModel()); n != tt.forModel {
					t.Fatalf("model sees %d messages", n)
				}
				if len(got.Todos) != tt.todos || got.ContextUsed() != tt.context || got.UsageNow() != v1.Usage {
					t.Fatalf("todos %v context %d usage %+v", got.Todos, got.ContextUsed(), got.UsageNow())
				}
				if got.Title != v1.Title || got.Mode != v1.Mode || got.Provider != v1.Provider || got.Status != v1.Status ||
					got.ParentID != v1.ParentID || got.ProjectID != v1.ProjectID || !got.Updated.Equal(v1.Updated) {
					t.Fatalf("fields %+v", got)
				}
			}
			if tt.comps > 0 {
				c := s.CompactionsNow()[0]
				if c.At != v1.Compactions[0].At || c.Summary != v1.Compactions[0].Summary {
					t.Fatalf("compaction %+v", c)
				}
			}
			if s.Repair() != tt.openCalls {
				t.Fatal("repair after migration")
			}
			if _, err := os.Stat(st.v1File(tt.id)); !os.IsNotExist(err) {
				t.Fatal("the old file is still in place")
			}
			if _, err := os.Stat(filepath.Join(st.dir, "v1", tt.id+".json")); err != nil {
				t.Fatal("no backup of the old file")
			}
			if l := st.List(); len(l) != 1 {
				t.Fatalf("listed after: %+v", l)
			}
		})
	}
}

func TestBranchAndResend(t *testing.T) {
	s, ids := twoTurns()
	// Edit u2 and send again: back to its parent, then a new message.
	if err := s.Branch(ids[1]); err != nil {
		t.Fatal(err)
	}
	if g := texts(s.Snapshot()); !reflect.DeepEqual(g, []string{"u1", "a1"}) {
		t.Fatalf("after branch %q", g)
	}
	s.addUserText("u2 again")
	s.appendReply(at(say("a2 again")), provider.Usage{Input: 5})
	if g := texts(s.Snapshot()); !reflect.DeepEqual(g, []string{"u1", "a1", "u2 again", "a2 again"}) {
		t.Fatalf("new branch %q", g)
	}
	if kids := s.Children(ids[1]); len(kids) != 2 || kids[0].ID != ids[2] {
		t.Fatalf("children %+v", kids)
	}
	got := reload(t, NewStore(t.TempDir()), s)
	if !reflect.DeepEqual(texts(got.Snapshot()), texts(s.Snapshot())) || len(got.Entries()) != 6 {
		t.Fatalf("reloaded %q, %d entries", texts(got.Snapshot()), len(got.Entries()))
	}
	// The old branch is still there to go back to.
	if err := got.Branch(ids[3]); err != nil {
		t.Fatal(err)
	}
	if g := texts(got.Snapshot()); !reflect.DeepEqual(g, []string{"u1", "a1", "u2", "a2"}) {
		t.Fatalf("old branch %q", g)
	}
	if err := got.Branch("nope"); err != ErrNoEntry {
		t.Fatalf("unknown entry: %v", err)
	}
}

func TestUndoKeepsBranch(t *testing.T) {
	s, ids := twoTurns()
	e, text, ok := s.LastPrompt()
	if !ok || e.ID != ids[2] || text != "u2" {
		t.Fatalf("last prompt %+v %q", e, text)
	}
	if err := s.Branch(e.ParentID); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Snapshot()); n != 2 {
		t.Fatalf("%d messages after undo", n)
	}
	if _, ok := s.Entry(ids[3]); !ok {
		t.Fatal("undo dropped the old turn")
	}
	got := reload(t, NewStore(t.TempDir()), s)
	if got.Head != ids[1] || len(got.Entries()) != 4 {
		t.Fatalf("head %s, %d entries", got.Head, len(got.Entries()))
	}
}

func TestFork(t *testing.T) {
	s, ids := twoTurns()
	s.appendReply(at(say("a3")), provider.Usage{Input: 100, Cost: 2})
	if err := s.Label(ids[1], "good point"); err != nil {
		t.Fatal(err)
	}
	f, err := s.Fork(ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if f.ID == s.ID || f.ForkOf == nil || f.ForkOf.Session != s.ID || f.ForkOf.Entry != ids[1] {
		t.Fatalf("fork of %+v", f.ForkOf)
	}
	if g := texts(f.Snapshot()); !reflect.DeepEqual(g, []string{"u1", "a1"}) {
		t.Fatalf("fork has %q", g)
	}
	if f.Labels()[ids[1]] != "good point" {
		t.Fatal("labels on the copied path are lost")
	}
	f.addUserText("other idea")
	if len(s.Snapshot()) != 5 {
		t.Fatal("the fork changed its source")
	}
	st := NewStore(t.TempDir())
	_ = st.Save(s)
	got := reload(t, st, f)
	if g := texts(got.Snapshot()); !reflect.DeepEqual(g, []string{"u1", "a1", "other idea"}) {
		t.Fatalf("reloaded fork %q", g)
	}
	if got.UsageNow().Cost != 0 {
		t.Fatalf("the fork counts its source's cost: %+v", got.UsageNow())
	}
	var sum Summary
	for _, x := range st.List() {
		if x.ID == f.ID {
			sum = x
		}
	}
	if sum.ForkOf != s.ID || sum.Turns != 2 {
		t.Fatalf("summary %+v", sum)
	}
	if _, err := s.Fork("nope"); err != ErrNoEntry {
		t.Fatalf("unknown entry: %v", err)
	}
}

func TestLabels(t *testing.T) {
	s, ids := twoTurns()
	st := NewStore(t.TempDir())
	_ = s.Label(ids[0], "start")
	_ = s.Label(ids[2], "parser")
	got := reload(t, st, s)
	if l := got.Labels(); len(l) != 2 || l[ids[2]] != "parser" {
		t.Fatalf("labels %v", l)
	}
	_ = got.Label(ids[2], " ")
	got = reload(t, st, got)
	if l := got.Labels(); len(l) != 1 || l[ids[0]] != "start" {
		t.Fatalf("after removing %v", l)
	}
	if err := s.Label("nope", "x"); err != ErrNoEntry {
		t.Fatalf("unknown entry: %v", err)
	}
}

func TestStatePerBranch(t *testing.T) {
	s := NewSession("auto", "p")
	s.SetModel("auto", "p", "m1", "")
	s.append(ut("u1"))
	s.appendReply(at(say("a1")), provider.Usage{Input: 100})
	fork := s.Head
	s.SetTodos([]tools.Todo{{Text: "x", Status: "pending"}})
	s.SetModel("plan", "q", "m2", "low")
	s.append(ut("u2"))
	s.appendReply(at(say("a2")), provider.Usage{Input: 300, Output: 20})
	compactAt(s, 2, "S")
	tests := []struct {
		name       string
		head       string
		context    int64
		todos      int
		mode, prov string
		effort     string
		forModel   int
	}{
		{"new branch", s.Head, 0, 1, "plan", "q", "low", 2},
		{"before todos", fork, 100, 0, "auto", "p", "", 2},
	}
	st := NewStore(t.TempDir())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Branch(tt.head); err != nil {
				t.Fatal(err)
			}
			for _, got := range []*Session{s, reload(t, st, s)} {
				want := tt.context
				if tt.name == "new branch" {
					want = s.CompactionsNow()[0].TokensAfter
				}
				if got.ContextUsed() != want || len(got.Todos) != tt.todos || got.Mode != tt.mode ||
					got.Provider != tt.prov || got.Effort != tt.effort || len(got.ForModel()) != tt.forModel {
					t.Fatalf("context %d todos %v mode %s/%s/%s model sees %d", got.ContextUsed(), got.Todos,
						got.Mode, got.Provider, got.Effort, len(got.ForModel()))
				}
			}
		})
	}
	if u := s.UsageNow(); u.Input != 400 || u.Output != 20 {
		t.Fatalf("totals cover every branch: %+v", u)
	}
}

func TestSaveAppends(t *testing.T) {
	s, _ := twoTurns()
	st := NewStore(t.TempDir())
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(st.file(s.ID))
	s.append(ut("u3"))
	s.SetStatus(StatusRunning)
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(st.file(s.ID))
	if !bytes.HasPrefix(after, before) || bytes.Count(after[len(before):], []byte("\n")) != 2 {
		t.Fatalf("save rewrote the file or added %q", after[len(before):])
	}
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(st.file(s.ID)); len(again) != len(after) {
		t.Fatal("a save with no changes wrote")
	}
}

func TestSaveRewritesMissingFile(t *testing.T) {
	s, _ := twoTurns()
	st := NewStore(t.TempDir())
	_ = st.Save(s)
	_ = os.Remove(st.file(s.ID))
	s.append(ut("u3"))
	got := reload(t, st, s)
	if n := len(got.Snapshot()); n != 5 {
		t.Fatalf("%d messages", n)
	}
}

func TestTruncatedLastLine(t *testing.T) {
	s, _ := twoTurns()
	st := NewStore(t.TempDir())
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	s.append(ut("u3 is long enough to cut"))
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	path := st.file(s.ID)
	data, _ := os.ReadFile(path)
	for _, cut := range []int{len(data) - 20, len(data) - 1} {
		if err := os.WriteFile(path, data[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := st.Load(s.ID)
		if err != nil {
			t.Fatalf("cut at %d: %v", cut, err)
		}
		want := 4 // the cut line is gone
		if cut == len(data)-1 {
			want = 5 // only its newline is gone
		}
		if n := len(got.Snapshot()); n != want {
			t.Fatalf("cut at %d: %d messages", cut, n)
		}
		got.append(ut("after the crash"))
		if err := st.Save(got); err != nil {
			t.Fatal(err)
		}
		again, err := st.Load(s.ID)
		if err != nil {
			t.Fatalf("cut at %d, after saving: %v", cut, err)
		}
		if g := texts(again.Snapshot()); g[len(g)-1] != "after the crash" || len(g) != want+1 {
			t.Fatalf("cut at %d: %q", cut, g)
		}
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"header\",\"v\":2}\nnot json\n{\"type\":\"state\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(s.ID); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("a broken line in the middle: %v", err)
	}
}

func TestAddUserTextReplacesEntry(t *testing.T) {
	s := NewSession("auto", "p")
	s.append(ut("go"))
	s.append(at(call("c1", "shell", `{}`)))
	s.append(tr("c1"))
	st := NewStore(t.TempDir())
	_ = st.Save(s)
	s.addUserText("continue")
	got := reload(t, st, s)
	msgs := got.Snapshot()
	if len(msgs) != 3 || len(got.Entries()) != 3 || len(msgs[2].Blocks) != 2 || msgs[2].Blocks[1].Text != "continue" {
		t.Fatalf("messages %+v", msgs)
	}
	if e, text, _ := got.LastPrompt(); text != "continue" || e.ID != got.Head {
		t.Fatalf("last prompt %q", text)
	}
}

func TestRunAfterBranchToToolCall(t *testing.T) {
	h := newAgent(t, "auto", []provider.Block{call("c1", "todo", `{"items":[]}`)}, []provider.Block{say("done")}, []provider.Block{say("again")})
	h.run(t, "start")
	ids := h.session.MessageIDs()
	if err := h.session.Branch(ids[1]); err != nil { // the assistant's call
		t.Fatal(err)
	}
	h.run(t, "do something else")
	msgs := h.session.Snapshot()
	if len(msgs) != 4 || msgs[2].Blocks[0].Text != branchedText || msgs[2].Blocks[1].Text != "do something else" {
		t.Fatalf("messages %+v", msgs)
	}
}

func TestSummarizeBranch(t *testing.T) {
	s, ids := twoTurns()
	leaf := s.Head
	_ = s.Branch(ids[1])
	fc := &fakeCompactor{}
	a := &Agent{Compaction: &CompactConfig{Compactor: fc}}
	if err := a.SummarizeBranch(context.Background(), s, leaf, 3); err != nil || len(fc.asked) != 0 {
		t.Fatalf("a short branch: %v, asked %d", err, len(fc.asked))
	}
	if err := a.SummarizeBranch(context.Background(), s, leaf, 2); err != nil {
		t.Fatal(err)
	}
	if !fc.asked[0].Branch || len(fc.asked[0].Messages) != 2 {
		t.Fatalf("asked %+v", fc.asked[0])
	}
	got := reload(t, NewStore(t.TempDir()), s)
	if b := got.BranchSummaries()[leaf]; b.Text != "SUMMARY A" || got.UsageNow().Cost != 0.5 {
		t.Fatalf("summaries %v, usage %+v", got.BranchSummaries(), got.UsageNow())
	}
}

func TestAddUserTextKeepsSharedEntry(t *testing.T) {
	s := NewSession("auto", "p")
	s.append(ut("go"))
	s.append(at(call("c1", "shell", `{}`)))
	s.append(tr("c1"))
	results := s.Head
	s.append(at(say("done")))
	_ = s.Branch(results)
	s.addUserText("instead")
	if g := texts(s.Snapshot()); len(g) != 3 || !strings.HasSuffix(g[2], "instead") {
		t.Fatalf("new path %q", g)
	}
	if e, _ := s.Entry(results); len(e.Message.Blocks) != 1 {
		t.Fatal("the shared message changed")
	}
	e, _ := s.Entry(results)
	if s.Head == results || len(s.Children(e.ParentID)) != 2 {
		t.Fatalf("head %s", s.Head)
	}
}
