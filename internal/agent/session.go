package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
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

// Session is one conversation with the agent.
type Session struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Mode     string `json:"mode"`
	Provider string `json:"provider"`
	// Root is the workspace the session works in.
	Root string `json:"root,omitempty"`
	// ProjectID is the project whose context the session starts with.
	ProjectID string `json:"project,omitempty"`
	// ParentID is the session that started this one as a sub-agent.
	ParentID string    `json:"parent,omitempty"`
	Status   string    `json:"status,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Messages []provider.Message `json:"messages"`
	// Times holds when each message joined, for replay. Sessions saved
	// before it existed have none.
	Times []time.Time    `json:"times,omitempty"`
	Usage provider.Usage `json:"usage"`
	Todos []tools.Todo   `json:"todos,omitempty"`

	mu sync.Mutex
}

// NewSession starts an empty session.
func NewSession(mode, prov string) *Session {
	var b [6]byte
	_, _ = rand.Read(b[:])
	t := now()
	return &Session{ID: t.Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), Mode: mode, Provider: prov, Created: t, Updated: t}
}

// append adds a message; the history is never rewritten.
func (s *Session) append(m provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := now()
	for len(s.Times) < len(s.Messages) {
		s.Times = append(s.Times, time.Time{})
	}
	s.Messages = append(s.Messages, m)
	s.Times = append(s.Times, t)
	s.Updated = t
	s.titleFrom(m)
}

// addUserText puts text in the last message when it is the user's and has
// not been answered yet, such as tool results of a cancelled turn, and in
// a new user message otherwise. Two user messages in a row are refused by
// some providers.
func (s *Session) addUserText(text string) {
	s.mu.Lock()
	if n := len(s.Messages); n > 0 && s.Messages[n-1].Role == provider.User {
		last := &s.Messages[n-1]
		last.Blocks = append(last.Blocks, provider.Block{Type: provider.Text, Text: text})
		s.Updated = now()
		s.titleFrom(*last)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.append(provider.Message{Role: provider.User, Blocks: []provider.Block{{Type: provider.Text, Text: text}}})
}

func (s *Session) titleFrom(m provider.Message) {
	if s.Title == "" && m.Role == provider.User {
		if t := strings.TrimSpace(m.TextOf()); t != "" {
			s.Title = firstLine(t, 80)
		}
	}
}

// AddUsage counts one call's tokens.
func (s *Session) AddUsage(u provider.Usage) {
	s.mu.Lock()
	s.Usage.Add(u)
	s.mu.Unlock()
}

// UsageNow returns the session's token totals.
func (s *Session) UsageNow() provider.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Usage
}

// Snapshot copies the messages for display.
func (s *Session) Snapshot() []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.Message(nil), s.Messages...)
}

// TimesNow copies when each message joined; zero for unknown.
func (s *Session) TimesNow() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]time.Time(nil), s.Times...)
	for len(out) < len(s.Messages) {
		out = append(out, time.Time{})
	}
	return out
}

// SetStatus records whether a turn is running.
func (s *Session) SetStatus(v string) {
	s.mu.Lock()
	s.Status = v
	s.mu.Unlock()
}

// StatusNow returns the session's status.
func (s *Session) StatusNow() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status
}

// SetTitle renames the session.
func (s *Session) SetTitle(t string) {
	s.mu.Lock()
	s.Title = t
	s.mu.Unlock()
}

// Repair answers tool calls left without results, as when hm stopped in
// the middle of a turn, so the history is valid to send again. It reports
// whether it changed anything.
func (s *Session) Repair() bool {
	s.mu.Lock()
	n := len(s.Messages)
	if n == 0 || s.Messages[n-1].Role != provider.Assistant {
		s.mu.Unlock()
		return false
	}
	calls := s.Messages[n-1].ToolUses()
	s.mu.Unlock()
	if len(calls) == 0 {
		return false
	}
	s.mu.Lock()
	updated := s.Updated
	s.mu.Unlock()
	results := make([]provider.Block, 0, len(calls))
	for _, c := range calls {
		results = append(results, provider.Block{Type: provider.ToolResult, ToolID: c.ToolID, ToolName: c.ToolName,
			Text: "Interrupted: hm stopped before this call finished", IsError: true})
	}
	s.append(provider.Message{Role: provider.User, Blocks: results})
	s.mu.Lock()
	s.Updated = updated // the work stopped then, not now
	s.mu.Unlock()
	return true
}

func firstLine(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// Store keeps the sessions of every workspace under one folder.
type Store struct {
	dir string
	// mu guards writes, which can come from several running sessions.
	mu sync.Mutex
}

// NewStore returns the session store under state.
func NewStore(state string) *Store {
	return &Store{dir: filepath.Join(state, "sessions")}
}

// Migrate moves root's sessions out of the per-workspace folder that older
// versions kept them in, marking each with root.
func (st *Store) Migrate(root string) error {
	sum := sha256.Sum256([]byte(root))
	old := filepath.Join(st.dir, hex.EncodeToString(sum[:8]))
	entries, err := os.ReadDir(old)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(old, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		if s.Root == "" {
			s.Root = root
		}
		if err := st.Save(&s); err != nil {
			return err
		}
		_ = os.Remove(path)
	}
	_ = os.Remove(old) // only when empty
	return nil
}

// Save writes s.
func (st *Store) Save(s *Session) error {
	s.mu.Lock()
	data, err := json.Marshal(s)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := os.MkdirAll(st.dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(st.dir, s.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads a session.
func (st *Store) Load(id string) (*Session, error) {
	data, err := os.ReadFile(filepath.Join(st.dir, filepath.Base(id)+".json"))
	if err != nil {
		return nil, err
	}
	var s Session
	return &s, json.Unmarshal(data, &s)
}

// Delete removes a session.
func (st *Store) Delete(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	err := os.Remove(filepath.Join(st.dir, filepath.Base(id)+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Summary describes a saved session for the list.
type Summary struct {
	ID, Title, Mode, Provider string
	Root, ProjectID, ParentID string
	Status                    string
	Updated                   time.Time
	// Turns counts the user's messages.
	Turns int
	Cost  float64
}

// List returns saved sessions, newest first.
func (st *Store) List() []Summary {
	entries, _ := os.ReadDir(st.dir)
	var out []Summary
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		s, err := st.Load(id)
		if err != nil {
			continue
		}
		turns := 0
		for _, m := range s.Messages {
			if m.Role == provider.User && strings.TrimSpace(m.TextOf()) != "" {
				turns++
			}
		}
		out = append(out, Summary{ID: s.ID, Title: s.Title, Mode: s.Mode, Provider: s.Provider,
			Root: s.Root, ProjectID: s.ProjectID, ParentID: s.ParentID, Status: s.Status,
			Updated: s.Updated, Turns: turns, Cost: s.Usage.Cost})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}
