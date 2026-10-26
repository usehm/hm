package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// Session is one conversation with the agent.
type Session struct {
	ID       string             `json:"id"`
	Title    string             `json:"title"`
	Mode     string             `json:"mode"`
	Provider string             `json:"provider"`
	Created  time.Time          `json:"created"`
	Updated  time.Time          `json:"updated"`
	Messages []provider.Message `json:"messages"`
	Usage    provider.Usage     `json:"usage"`
	Todos    []tools.Todo       `json:"todos,omitempty"`

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
	s.Messages = append(s.Messages, m)
	s.Updated = now()
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

func firstLine(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// Store keeps a workspace's sessions under dir.
type Store struct{ dir string }

// NewStore returns the session store for a workspace root, under state.
func NewStore(state, root string) *Store {
	sum := sha256.Sum256([]byte(root))
	return &Store{dir: filepath.Join(state, "sessions", hex.EncodeToString(sum[:8]))}
}

// Save writes s.
func (st *Store) Save(s *Session) error {
	s.mu.Lock()
	data, err := json.Marshal(s)
	s.mu.Unlock()
	if err != nil {
		return err
	}
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

// Summary describes a saved session for the list.
type Summary struct {
	ID, Title, Mode, Provider string
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
		if !ok {
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
			Updated: s.Updated, Turns: turns, Cost: s.Usage.Cost})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}
