package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// Session statuses. A session saved as running when hm stops was cut off
// by a crash or a quit; it loads as interrupted.
const (
	StatusIdle        = ""
	StatusRunning     = "running"
	StatusInterrupted = "interrupted"
)

// Entry kinds. Every entry has a parent, so a session is a tree; the
// conversation the agent continues is the path from the root to Head.
const (
	KindMessage     = "message"
	KindCompaction  = "compaction"
	KindModeChange  = "mode_change"
	KindModelChange = "model_change"
	KindTodos       = "todos"
)

// Session is one conversation with the agent. Going back to an earlier
// entry and going on from there starts a branch; the old one stays, so
// nothing is ever lost.
type Session struct {
	ID       string
	Title    string
	Mode     string
	Provider string
	// Model is the provider's model the session last ran with; empty for
	// the provider's default.
	Model string
	// Root is the workspace the session works in.
	Root string
	// ProjectID is the project whose context the session starts with.
	ProjectID string
	// ParentID is the session that started this one as a sub-agent.
	ParentID string
	// ForkOf is where this session was forked from; nil when it was not.
	ForkOf  *ForkRef
	Status  string
	Created time.Time
	Updated time.Time
	// Head is the entry the next one follows; empty for a new session.
	Head string
	// Usage totals the session's model calls on every branch.
	Usage provider.Usage
	// Context is the tokens the last model call on the active path read
	// and wrote, about what the next one sends.
	Context int64
	// Todos is the task list as the active path left it.
	Todos []tools.Todo

	entries []Entry
	index   map[string]int // entry id → position in entries
	labels  map[string]string
	notes   map[string]BranchSummary // by the abandoned leaf
	// legacy is what a session migrated from the old format had spent and
	// filled before it had entries to hold it.
	legacy        provider.Usage
	legacyContext int64
	// path caches the active path as positions in entries.
	path   []int
	pathOK bool

	file fileState
	mu   sync.Mutex
}

// ForkRef names the session and entry a fork started from.
type ForkRef struct {
	Session string `json:"session"`
	Entry   string `json:"entry,omitempty"`
}

// Entry is one node of the session's tree.
type Entry struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent_id,omitempty"`
	Kind     string    `json:"kind"`
	Time     time.Time `json:"time"`
	// Message is set for KindMessage; Usage for the model's replies and
	// for compactions, whose summary was a model call too.
	Message    *provider.Message `json:"message,omitempty"`
	Usage      *provider.Usage   `json:"usage,omitempty"`
	Compaction *Compaction       `json:"compaction,omitempty"`
	// Mode, Provider and Model are what a mode or model change set.
	Mode     string `json:"mode,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Todos is the task list a KindTodos entry set; empty clears it.
	Todos []tools.Todo `json:"todos,omitempty"`
	// Forked marks an entry copied from the session this one was forked
	// from; its cost was counted there.
	Forked bool `json:"forked,omitempty"`
}

// Compaction replaces the messages before KeptFrom with a summary in what
// the model sees.
type Compaction struct {
	// KeptFrom is the first message kept. At is its index among the
	// active path's messages, filled in by CompactionsNow.
	KeptFrom string `json:"kept_from"`
	At       int    `json:"-"`
	Summary  string `json:"summary"`
	// TokensBefore is the context used before; TokensAfter is an estimate
	// until the next model call measures it.
	TokensBefore int64     `json:"tokens_before"`
	TokensAfter  int64     `json:"tokens_after"`
	Model        string    `json:"model,omitempty"`
	Time         time.Time `json:"time"`
}

// BranchSummary says in a few lines what a branch the user left did.
type BranchSummary struct {
	Text  string
	Model string
	Usage provider.Usage
}

// NewSession starts an empty session.
func NewSession(mode, prov string) *Session {
	var b [6]byte
	_, _ = rand.Read(b[:])
	t := now()
	return &Session{ID: t.Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), Mode: mode, Provider: prov, Created: t, Updated: t}
}

// newEntryID is unique within the session.
func (s *Session) newEntryID() string {
	for {
		var b [4]byte
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		if _, ok := s.index[id]; !ok {
			return id
		}
	}
}

// put stores e, replacing an entry with its ID, and queues it for the
// file. It does not move Head.
func (s *Session) put(e Entry) {
	if s.index == nil {
		s.index = map[string]int{}
	}
	if i, ok := s.index[e.ID]; ok {
		if old := s.entries[i]; old.Usage != nil && !old.Forked {
			u := *old.Usage
			s.Usage.Add(provider.Usage{Input: -u.Input, Output: -u.Output, CacheRead: -u.CacheRead,
				CacheWrite: -u.CacheWrite, Cost: -u.Cost})
		}
		s.entries[i] = e
	} else {
		s.index[e.ID] = len(s.entries)
		s.entries = append(s.entries, e)
	}
	if e.Usage != nil && !e.Forked {
		s.Usage.Add(*e.Usage)
	}
	s.file.pending = append(s.file.pending, entryRecord{Type: recEntry, Entry: e})
}

// add puts e after Head and makes it the new Head.
func (s *Session) add(e Entry) {
	e.ID, e.ParentID = s.newEntryID(), s.Head
	if e.Time.IsZero() {
		e.Time = now()
	}
	s.put(e)
	s.Head = e.ID
	if s.pathOK {
		s.path = append(s.path, s.index[e.ID])
	}
	s.Updated = e.Time
	s.settle()
}

// pathTo is the positions of the entries from the root to id.
func (s *Session) pathTo(id string) []int {
	var p []int
	for id != "" && len(p) <= len(s.entries) {
		i, ok := s.index[id]
		if !ok {
			break
		}
		p = append(p, i)
		id = s.entries[i].ParentID
	}
	slices.Reverse(p)
	return p
}

// activePath is the cached path to Head.
func (s *Session) activePath() []int {
	if !s.pathOK {
		s.path, s.pathOK = s.pathTo(s.Head), true
	}
	return s.path
}

// settle works out what depends on the active path: the context used,
// the task list, and the mode and model in effect.
func (s *Session) settle() {
	p := s.activePath()
	s.Context, s.Todos = s.legacyContext, nil
	ctxDone, todosDone, modeDone, modelDone := false, false, false, false
	for k := len(p) - 1; k >= 0; k-- {
		e := &s.entries[p[k]]
		switch e.Kind {
		case KindMessage:
			if !ctxDone && e.Usage != nil {
				if n := e.Usage.Input + e.Usage.CacheRead + e.Usage.CacheWrite + e.Usage.Output; n > 0 {
					s.Context, ctxDone = n, true
				}
			}
		case KindCompaction:
			if !ctxDone && e.Compaction != nil {
				s.Context, ctxDone = e.Compaction.TokensAfter, true
			}
		case KindTodos:
			if !todosDone {
				s.Todos, todosDone = slices.Clone(e.Todos), true
			}
		case KindModeChange:
			if !modeDone {
				s.Mode, modeDone = e.Mode, true
			}
		case KindModelChange:
			if !modelDone {
				s.Provider, s.Model, modelDone = e.Provider, e.Model, true
			}
		}
	}
}

// messages are the active path's messages with their entries.
func (s *Session) messages() ([]provider.Message, []*Entry) {
	p := s.activePath()
	msgs := make([]provider.Message, 0, len(p))
	ents := make([]*Entry, 0, len(p))
	for _, i := range p {
		if e := &s.entries[i]; e.Kind == KindMessage && e.Message != nil {
			msgs = append(msgs, *e.Message)
			ents = append(ents, e)
		}
	}
	return msgs, ents
}

// append adds a message after Head.
func (s *Session) append(m provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(Entry{Kind: KindMessage, Message: &m})
	s.titleFrom(m)
}

// Append adds messages after Head, as a turn does; for building sessions
// outside a turn.
func (s *Session) Append(msgs ...provider.Message) {
	for _, m := range msgs {
		s.append(m)
	}
}

// appendReply adds the model's reply and what the call cost.
func (s *Session) appendReply(m provider.Message, u provider.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(Entry{Kind: KindMessage, Message: &m, Usage: &u})
}

// addUserText puts text in the last message when it is the user's and has
// not been answered yet, such as tool results of a cancelled turn, and in
// a new user message otherwise. Two user messages in a row are refused by
// some providers.
func (s *Session) addUserText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	block := provider.Block{Type: provider.Text, Text: text}
	if _, ents := s.messages(); len(ents) > 0 && ents[len(ents)-1].Message.Role == provider.User {
		e := *ents[len(ents)-1]
		m := *e.Message
		m.Blocks = append(slices.Clone(m.Blocks), block)
		e.Message = &m
		if s.hasChildren(e.ID) {
			// Another branch goes on from it: change a copy next to it.
			s.Head, s.pathOK = e.ParentID, false
			s.add(Entry{Kind: KindMessage, Message: &m})
		} else {
			s.put(e) // the file gets the message again; the later line wins
			s.Updated = now()
		}
		s.titleFrom(m)
		return
	}
	m := provider.Message{Role: provider.User, Blocks: []provider.Block{block}}
	s.add(Entry{Kind: KindMessage, Message: &m})
	s.titleFrom(m)
}

func (s *Session) hasChildren(id string) bool {
	for _, e := range s.entries {
		if e.ParentID == id {
			return true
		}
	}
	return false
}

func (s *Session) titleFrom(m provider.Message) {
	if s.Title == "" && m.Role == provider.User {
		if t := strings.TrimSpace(m.TextOf()); t != "" {
			s.Title = firstLine(t, 80)
			s.file.state = true
		}
	}
}

// ContextUsed is how much of the model's window the conversation fills,
// as of the last model call; 0 before the first.
func (s *Session) ContextUsed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Context
}

// UsageNow returns the session's token totals.
func (s *Session) UsageNow() provider.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Usage
}

// Snapshot copies the active path's messages, for display and replay.
func (s *Session) Snapshot() []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs, _ := s.messages()
	return msgs
}

// MessageIDs are the entry IDs of Snapshot's messages, in order.
func (s *Session) MessageIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ents := s.messages()
	out := make([]string, len(ents))
	for i, e := range ents {
		out[i] = e.ID
	}
	return out
}

// ForModel is the conversation as the model sees it: after a compaction,
// its summary followed by the messages it kept.
func (s *Session) ForModel() []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs, ents := s.messages()
	c, at := s.lastCompaction(ents)
	if at < 0 {
		return msgs
	}
	return withSummary(c.Summary, msgs[at:])
}

// lastCompaction is the active path's latest compaction and the index of
// its first kept message in ents; -1 when there is none.
func (s *Session) lastCompaction(ents []*Entry) (Compaction, int) {
	p := s.activePath()
	for k := len(p) - 1; k >= 0; k-- {
		e := s.entries[p[k]]
		if e.Kind != KindCompaction || e.Compaction == nil {
			continue
		}
		for i, m := range ents {
			if m.ID == e.Compaction.KeptFrom {
				c := *e.Compaction
				c.At = i
				return c, i
			}
		}
	}
	return Compaction{}, -1
}

// CompactionsNow copies the active path's compactions, each with At set.
func (s *Session) CompactionsNow() []Compaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ents := s.messages()
	at := map[string]int{}
	for i, e := range ents {
		at[e.ID] = i
	}
	var out []Compaction
	for _, i := range s.activePath() {
		e := s.entries[i]
		if e.Kind != KindCompaction || e.Compaction == nil {
			continue
		}
		if k, ok := at[e.Compaction.KeptFrom]; ok {
			c := *e.Compaction
			c.At = k
			out = append(out, c)
		}
	}
	return out
}

// addCompaction records c after Head; the context used becomes its
// estimate.
func (s *Session) addCompaction(c Compaction, u provider.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.add(Entry{Kind: KindCompaction, Compaction: &c, Usage: &u, Time: c.Time})
}

// TimesNow copies when each of Snapshot's messages joined; zero for
// unknown.
func (s *Session) TimesNow() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ents := s.messages()
	out := make([]time.Time, len(ents))
	for i, e := range ents {
		out[i] = e.Time
	}
	return out
}

// SetTodos records the task list when it changed.
func (s *Session) SetTodos(items []tools.Todo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Equal(items, s.Todos) {
		return
	}
	s.add(Entry{Kind: KindTodos, Todos: slices.Clone(items)})
}

// SetModel records the mode, provider and model the next turn uses. Each
// change is an entry, so every branch keeps its own and going back to an
// earlier point brings back what was used there.
func (s *Session) SetModel(mode, prov, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cur Entry // the path's latest of each
	var hasMode, hasModel bool
	p := s.activePath()
	for k := len(p) - 1; k >= 0 && !(hasMode && hasModel); k-- {
		switch e := s.entries[p[k]]; {
		case e.Kind == KindModeChange && !hasMode:
			cur.Mode, hasMode = e.Mode, true
		case e.Kind == KindModelChange && !hasModel:
			cur.Provider, cur.Model, hasModel = e.Provider, e.Model, true
		}
	}
	if !hasMode || mode != cur.Mode {
		s.add(Entry{Kind: KindModeChange, Mode: mode})
	}
	if !hasModel || prov != cur.Provider || model != cur.Model {
		s.add(Entry{Kind: KindModelChange, Provider: prov, Model: model})
	}
	s.file.state = true
}

// SetStatus records whether a turn is running.
func (s *Session) SetStatus(v string) {
	s.mu.Lock()
	s.Status, s.file.state = v, true
	s.mu.Unlock()
}

// StatusNow returns the session's status.
func (s *Session) StatusNow() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status
}

// Meta copies the session's title and when it was created and updated.
func (s *Session) Meta() (title string, created, updated time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Title, s.Created, s.Updated
}

// SetTitle renames the session.
func (s *Session) SetTitle(t string) {
	s.mu.Lock()
	s.Title, s.file.state = t, true
	s.mu.Unlock()
}

// Repair answers tool calls left without results, as when hm stopped in
// the middle of a turn, so the history is valid to send again. It reports
// whether it changed anything.
func (s *Session) Repair() bool {
	return s.answerOpenCalls("Interrupted: hm stopped before this call finished", true)
}

// answerOpenCalls gives each call of a last assistant message a result
// saying text. keepTime leaves Updated as it was.
func (s *Session) answerOpenCalls(text string, keepTime bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ents := s.messages()
	if len(ents) == 0 || ents[len(ents)-1].Message.Role != provider.Assistant {
		return false
	}
	calls := ents[len(ents)-1].Message.ToolUses()
	if len(calls) == 0 {
		return false
	}
	results := make([]provider.Block, 0, len(calls))
	for _, c := range calls {
		results = append(results, provider.Block{Type: provider.ToolResult, ToolID: c.ToolID, ToolName: c.ToolName,
			Text: text, IsError: true})
	}
	updated := s.Updated
	s.add(Entry{Kind: KindMessage, Message: &provider.Message{Role: provider.User, Blocks: results}})
	if keepTime {
		s.Updated, s.file.state = updated, true // the work stopped then, not now
	}
	return true
}

// ErrNoEntry is returned for an entry ID the session does not have.
var ErrNoEntry = errors.New("no such entry in this session")

func firstLine(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}
