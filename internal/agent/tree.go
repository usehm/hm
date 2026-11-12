package agent

import (
	"slices"
	"strings"

	"github.com/usehm/hm/internal/agent/provider"
)

// The session tree: Branch goes back to an earlier entry, so the next
// message starts a new branch next to the old one; Fork copies the path to
// an entry into a new session; labels bookmark entries.

// Path copies the entries from the root to Head.
func (s *Session) Path() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.activePath()
	out := make([]Entry, len(p))
	for k, i := range p {
		out[k] = s.entries[i]
	}
	return out
}

// Entries copies every entry, in the order they were added.
func (s *Session) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.entries)
}

// Entry returns the entry with id.
func (s *Session) Entry(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.index[id]
	if !ok {
		return Entry{}, false
	}
	return s.entries[i], true
}

// Children are the entries whose parent is id, oldest first; for "" the
// roots.
func (s *Session) Children(id string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.entries {
		if e.ParentID == id {
			out = append(out, e)
		}
	}
	return out
}

// Branch moves Head to id, so the next entry becomes a new child of it;
// "" goes back to before the first message. The path left behind stays.
func (s *Session) Branch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.index[id]; id != "" && !ok {
		return ErrNoEntry
	}
	s.Head, s.pathOK = id, false
	s.file.state = true
	s.Updated = now()
	s.settle()
	return nil
}

// Fork starts a new session holding the active path up to id ("" for an
// empty one), the source of which it records.
func (s *Session) Fork(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.index[id]; id != "" && !ok {
		return nil, ErrNoEntry
	}
	f := NewSession(s.Mode, s.Provider)
	f.Model, f.Title, f.Root, f.ProjectID = s.Model, s.Title, s.Root, s.ProjectID
	f.ForkOf = &ForkRef{Session: s.ID, Entry: id}
	f.legacyContext = s.legacyContext
	for _, i := range s.pathTo(id) {
		e := s.entries[i]
		e.Forked = true
		f.put(e)
		if l, ok := s.labels[e.ID]; ok {
			f.setLabel(e.ID, l)
		}
	}
	f.Head = id
	f.settle()
	return f, nil
}

// Label bookmarks entry id with text; empty text removes the label.
func (s *Session) Label(id, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.index[id]; !ok {
		return ErrNoEntry
	}
	s.setLabel(id, strings.TrimSpace(text))
	s.Updated = now()
	s.file.state = true
	return nil
}

func (s *Session) setLabel(id, text string) {
	if s.labels == nil {
		s.labels = map[string]string{}
	}
	if text == "" {
		delete(s.labels, id)
	} else {
		s.labels[id] = text
	}
	s.file.pending = append(s.file.pending, labelRecord{Type: recLabel, Target: id, Text: text})
}

// Labels copies the labels, by entry ID.
func (s *Session) Labels() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.labels))
	for k, v := range s.labels {
		out[k] = v
	}
	return out
}

// BranchSummaries copies the summaries of left branches, by their leaf.
func (s *Session) BranchSummaries() map[string]BranchSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]BranchSummary, len(s.notes))
	for k, v := range s.notes {
		out[k] = v
	}
	return out
}

func (s *Session) addBranchSummary(leaf string, b BranchSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setBranchSummary(leaf, b)
	s.Usage.Add(b.Usage)
}

func (s *Session) setBranchSummary(leaf string, b BranchSummary) {
	if s.notes == nil {
		s.notes = map[string]BranchSummary{}
	}
	s.notes[leaf] = b
	s.file.pending = append(s.file.pending, summaryRecord{Type: recBranchSummary, Target: leaf,
		Text: b.Text, Model: b.Model, Usage: b.Usage})
}

// Abandoned are the messages on the path to leaf that the active path
// does not share: what a branch the user left did.
func (s *Session) Abandoned(leaf string) []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	on := map[int]bool{}
	for _, i := range s.activePath() {
		on[i] = true
	}
	var out []provider.Message
	for _, i := range s.pathTo(leaf) {
		if e := s.entries[i]; !on[i] && e.Kind == KindMessage && e.Message != nil {
			out = append(out, *e.Message)
		}
	}
	return out
}

// LastPrompt is the active path's latest message the user typed: its
// entry and its text. ok is false when there is none.
func (s *Session) LastPrompt() (Entry, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ents := s.messages()
	for i := len(ents) - 1; i >= 0; i-- {
		if t := PromptText(*ents[i].Message); t != "" {
			return *ents[i], t, true
		}
	}
	return Entry{}, "", false
}

// ImageMark stands for a picture in a prompt's text.
const ImageMark = "[image]"

// PromptText is what the user typed in m: its text blocks, and ImageMark
// for each picture, without the tool results it may carry; "" for the
// model's messages.
func PromptText(m provider.Message) string {
	if m.Role != provider.User {
		return ""
	}
	var parts []string
	for _, b := range m.Blocks {
		switch {
		case b.Type == provider.Text && strings.TrimSpace(b.Text) != "":
			parts = append(parts, b.Text)
		case b.Type == provider.Image:
			parts = append(parts, ImageMark)
		}
	}
	return strings.Join(parts, "\n\n")
}
