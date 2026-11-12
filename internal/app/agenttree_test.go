package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usehm/hm/internal/agent"
	"github.com/usehm/hm/internal/agent/provider"
)

func userMsg(t string) provider.Message {
	return provider.Message{Role: provider.User, Blocks: []provider.Block{{Type: provider.Text, Text: t}}}
}

func botMsg(t string, calls ...string) provider.Message {
	m := provider.Message{Role: provider.Assistant}
	if t != "" {
		m.Blocks = append(m.Blocks, provider.Block{Type: provider.Text, Text: t})
	}
	for _, c := range calls {
		m.Blocks = append(m.Blocks, provider.Block{Type: provider.ToolUse, ToolID: c, ToolName: c, Input: []byte(`{}`)})
	}
	return m
}

func resultMsg(id string) provider.Message {
	return provider.Message{Role: provider.User, Blocks: []provider.Block{{Type: provider.ToolResult, ToolID: id, ToolName: id, Text: "ok"}}}
}

func TestTreeRows(t *testing.T) {
	s := agent.NewSession("auto", "p")
	s.Append(userMsg("fix the parser"), botMsg("Reading.", "read"), resultMsg("read"), botMsg("Fixed it."))
	ids := s.MessageIDs()
	s.Append(userMsg("now add tests"), botMsg("Added."))
	left := s.Head
	_ = s.Branch(ids[3])
	s.Append(userMsg("write docs instead\n\n<file path=\"a.go\">x</file>"), botMsg("Wrote README."))
	_ = s.Label(ids[0], "parser")
	notes := map[string]agent.BranchSummary{left: {Text: "Tried tests; they passed."}}

	tests := []struct {
		name   string
		filter int
		query  string
		want   []string // text@depth, "*" when on the path
	}{
		{"all", treeAll, "", []string{"*fix the parser@0", "*Reading. · called read@0", "*Fixed it.@0",
			"now add tests@1", "Added.@1", "note@1", "*write docs instead@1", "*Wrote README.@1"}},
		{"user", treeUser, "", []string{"*fix the parser@0", "now add tests@1", "note@1", "*write docs instead@1"}},
		{"assistant", treeAssistant, "", []string{"*Reading. · called read@0", "*Fixed it.@0", "Added.@1", "note@1", "*Wrote README.@1"}},
		{"tools", treeTools, "", []string{"*Reading. · called read@0", "*Results of read@0", "note@1"}},
		{"labels", treeLabels, "", []string{"*fix the parser@0"}},
		{"search a label", treeAll, "PARSER", []string{"*fix the parser@0"}},
		{"search a note", treeAll, "passed", []string{"note@1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, r := range treeRows(s.Entries(), s.Head, s.Labels(), notes, tt.filter, tt.query) {
				text := r.text
				if r.note != "" {
					text = "note"
				}
				if r.onPath {
					text = "*" + text
				}
				got = append(got, text+"@"+string(rune('0'+r.depth)))
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("rows\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
	if rows := treeRows(s.Entries(), s.Head, nil, nil, treeAll, ""); !rows[len(rows)-1].head {
		t.Fatal("the last row should be where the session is")
	}
}

func TestTurnEnd(t *testing.T) {
	s := agent.NewSession("auto", "p")
	s.Append(userMsg("one"), botMsg("", "read"), resultMsg("read"), botMsg("a"), userMsg("two"), botMsg("b"))
	ids := s.MessageIDs()
	path := s.Path()
	if end, ok := turnEnd(path, ids[0]); !ok || end != ids[3] {
		t.Fatalf("first turn ends at %s, %v", end, ok)
	}
	if _, ok := turnEnd(path, ids[4]); ok {
		t.Fatal("the latest turn has nothing after it")
	}
}

func TestFindEntry(t *testing.T) {
	s := agent.NewSession("auto", "p")
	s.Append(userMsg("one"), botMsg("a"))
	id := s.MessageIDs()[1]
	tests := []struct {
		arg, want string
		err       bool
	}{
		{id, id, false},
		{id[:6], id, false},
		{"zzzz", "", true},
		{"", "", true}, // a prefix of every entry
	}
	for _, tt := range tests {
		got, err := findEntry(s, tt.arg)
		if got != tt.want || (err != nil) != tt.err {
			t.Errorf("%q: %q %v", tt.arg, got, err)
		}
	}
}

// Editing an earlier message and sending it again keeps both branches.
func TestEditAndResendKeepsBothBranches(t *testing.T) {
	model := &fakeModel{}
	h, _ := agentProject(t, "anthropic", model)
	r := h.app.run()
	for _, msg := range []string{"first", "second"} {
		runTask(t, h, msg)
		h.until("turn end", 10*time.Second, func() bool { return !r.running })
	}
	ids := r.session.MessageIDs()
	second, _ := r.session.Entry(ids[2])
	h.app.editAndResend(r, second)
	if r.input != "second" || len(r.session.Snapshot()) != 2 {
		t.Fatalf("input %q, %d messages", r.input, len(r.session.Snapshot()))
	}
	r.input = "second, changed"
	h.app.sendAgent()
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	msgs := r.session.Snapshot()
	if len(msgs) != 4 || msgs[2].TextOf() != "second, changed" {
		t.Fatalf("messages %+v", msgs)
	}
	if kids := r.session.Children(second.ParentID); len(kids) != 2 {
		t.Fatalf("%d branches", len(kids))
	}
	sent := model.requests[len(model.requests)-1]["messages"].([]any)
	if len(sent) != 3 {
		t.Fatalf("the model saw %d messages, not the new branch's 3", len(sent))
	}

	// Saved with both branches; /tree shows them.
	saved, err := h.app.agent.store.Load(r.session.ID)
	if err != nil || len(saved.Children(second.ParentID)) != 2 {
		t.Fatalf("saved %v", err)
	}
	runTask(t, h, "/tree")
	h.frame()
	if !h.app.ctx.Dialogs().Open {
		t.Fatal("/tree did not open")
	}
	rows := treeRows(r.session.Entries(), r.session.Head, nil, nil, treeUser, "")
	if len(rows) != 3 {
		t.Fatalf("tree rows %+v", rows)
	}
	h.app.ctx.Dialogs().Close()

	// Branch here on the first message leaves the later turn behind.
	end, ok := turnEnd(r.session.Path(), ids[0])
	if !ok {
		t.Fatal("no end for the first turn")
	}
	h.app.branchTo(r, end)
	if n := len(r.session.Snapshot()); n != 2 {
		t.Fatalf("%d messages after branching", n)
	}
	h.frame()
}

func TestUndoRewindsAndRestores(t *testing.T) {
	model := &fakeModel{turns: [][]toolCall{
		nil,
		{{"edit", `{"path":"main.go","old_string":"func main() {}","new_string":"func main() { println(1) }"}`}},
		nil,
	}}
	h, root := agentProject(t, "anthropic", model)
	r := h.app.run()
	runTask(t, h, "ask first")
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	runTask(t, h, "make main print 1")
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	main := filepath.Join(root, "main.go")
	if got, _ := os.ReadFile(main); !strings.Contains(string(got), "println(1)") {
		t.Fatalf("not edited: %q", got)
	}
	before := len(r.session.Entries())
	h.app.undoAgentTurn(r)
	if got, _ := os.ReadFile(main); strings.Contains(string(got), "println(1)") {
		t.Fatalf("undo left %q", got)
	}
	if r.input != "make main print 1" || len(r.session.Snapshot()) != 2 || len(r.session.Entries()) != before {
		t.Fatalf("input %q, %d messages, %d entries", r.input, len(r.session.Snapshot()), len(r.session.Entries()))
	}
	h.app.undoAgentTurn(r)
	if len(r.session.Snapshot()) != 0 || r.input != "ask first" {
		t.Fatalf("second undo: %d messages, input %q", len(r.session.Snapshot()), r.input)
	}
}

func TestForkAndLabelCommands(t *testing.T) {
	h, _ := agentProject(t, "openai", &fakeModel{})
	r := h.app.run()
	for _, msg := range []string{"first", "second"} {
		runTask(t, h, msg)
		h.until("turn end", 10*time.Second, func() bool { return !r.running })
	}
	runTask(t, h, "/label good state")
	ids := r.session.MessageIDs()
	if r.session.Labels()[ids[3]] != "good state" {
		t.Fatalf("labels %v", r.session.Labels())
	}

	h.app.slashCommand(r, "/fork "+ids[2][:6]) // the user's second message
	f := h.app.run()
	if f == r || f.session.ForkOf == nil || f.session.ForkOf.Session != r.session.ID {
		t.Fatal("/fork did not open the forked session")
	}
	if n := len(f.session.Snapshot()); n != 2 || f.input != "second" {
		t.Fatalf("fork has %d messages, input %q", n, f.input)
	}
	h.app.agent.sessionsStale = true
	h.frame()
	var forkRow bool
	for _, s := range h.app.sessionList() {
		forkRow = forkRow || s.ForkOf == r.session.ID
	}
	if !forkRow || len(h.app.sidebarSessions(h.c)) != 2 {
		t.Fatal("the sidebar does not nest the fork under its source")
	}
	h.app.slashCommand(f, "/fork nope")
	if !strings.Contains(f.errText, "No entry") {
		t.Fatalf("error %q", f.errText)
	}
}
