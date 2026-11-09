package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// A session file holds one JSON object per line: a header, then entries,
// labels, branch summaries and state as they happen, so saving after each
// step appends a few lines instead of writing the whole file again. An
// entry whose ID comes again is replaced by the later line; the last state
// line wins. A last line cut off by a crash is left out on load.

// formatVersion is the session file format.
const formatVersion = 2

// Record types.
const (
	recHeader        = "header"
	recEntry         = "entry"
	recLabel         = "label"
	recBranchSummary = "branch_summary"
	recState         = "state"
)

type headerRecord struct {
	Type      string    `json:"type"`
	V         int       `json:"v"`
	ID        string    `json:"id"`
	Created   time.Time `json:"created"`
	Root      string    `json:"root,omitempty"`
	ProjectID string    `json:"project,omitempty"`
	ParentID  string    `json:"parent,omitempty"`
	ForkOf    *ForkRef  `json:"fork_of,omitempty"`
	// Legacy is what a session migrated from version 1 had spent and
	// filled, which no entry holds.
	LegacyUsage   *provider.Usage `json:"legacy_usage,omitempty"`
	LegacyContext int64           `json:"legacy_context,omitempty"`
}

type entryRecord struct {
	Type string `json:"type"`
	Entry
}

type labelRecord struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	Text   string `json:"text"`
}

type summaryRecord struct {
	Type   string         `json:"type"`
	Target string         `json:"target"`
	Text   string         `json:"text"`
	Model  string         `json:"model,omitempty"`
	Usage  provider.Usage `json:"usage"`
}

type stateRecord struct {
	Type     string    `json:"type"`
	Head     string    `json:"head"`
	Title    string    `json:"title,omitempty"`
	Mode     string    `json:"mode,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Model    string    `json:"model,omitempty"`
	Status   string    `json:"status,omitempty"`
	Updated  time.Time `json:"updated"`
}

// fileState is what the store knows of a session's file.
type fileState struct {
	// written says the file exists with this session's header; until then
	// a save writes the whole file.
	written bool
	pending []any // records not yet in the file
	state   bool  // the state changed since the last state line
	// size is where the last good line ends; nl says it has no newline.
	size int64
	nl   bool
}

func (s *Session) header() headerRecord {
	h := headerRecord{Type: recHeader, V: formatVersion, ID: s.ID, Created: s.Created, Root: s.Root,
		ProjectID: s.ProjectID, ParentID: s.ParentID, ForkOf: s.ForkOf, LegacyContext: s.legacyContext}
	if s.legacy != (provider.Usage{}) {
		u := s.legacy
		h.LegacyUsage = &u
	}
	return h
}

func (s *Session) stateRecord() stateRecord {
	return stateRecord{Type: recState, Head: s.Head, Title: s.Title, Mode: s.Mode, Provider: s.Provider,
		Model: s.Model, Status: s.Status, Updated: s.Updated}
}

// all is every record of s, for writing its file whole.
func (s *Session) all() []any {
	out := []any{s.header()}
	for _, e := range s.entries {
		out = append(out, entryRecord{Type: recEntry, Entry: e})
	}
	for id, t := range s.labels {
		out = append(out, labelRecord{Type: recLabel, Target: id, Text: t})
	}
	for id, b := range s.notes {
		out = append(out, summaryRecord{Type: recBranchSummary, Target: id, Text: b.Text, Model: b.Model, Usage: b.Usage})
	}
	return append(out, s.stateRecord())
}

// Lines is the session as its file holds it, one record per line, with
// every entry once; for exporting the raw session.
func (s *Session) Lines() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return lines(s.all())
}

func lines(recs []any) ([]byte, error) {
	var b bytes.Buffer
	for _, r := range recs {
		data, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

// decodeSession reads a version 2 file. A last line that does not parse,
// as when hm stopped while writing it, is left out.
func decodeSession(data []byte) (*Session, error) {
	s := &Session{}
	headed := false
	for off, n := 0, 1; off < len(data); n++ {
		end, next := len(data), len(data)
		if i := bytes.IndexByte(data[off:], '\n'); i >= 0 {
			end, next = off+i, off+i+1
		}
		line := bytes.TrimSpace(data[off:end])
		if len(line) > 0 {
			err := s.decodeLine(line, headed)
			if err != nil {
				if len(bytes.TrimSpace(data[next:])) == 0 && headed {
					break // the line a crash cut off
				}
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			headed = true
			s.file.size, s.file.nl = int64(next), next == end
		}
		off = next
	}
	if !headed {
		return nil, errors.New("empty session file")
	}
	s.pathOK = false
	s.settle()
	s.file.written, s.file.pending, s.file.state = true, nil, false
	return s, nil
}

func (s *Session) decodeLine(line []byte, headed bool) error {
	var kind struct {
		Type string `json:"type"`
		V    int    `json:"v"`
	}
	if err := json.Unmarshal(line, &kind); err != nil {
		return err
	}
	if !headed {
		if kind.Type != recHeader {
			return errors.New("no header")
		}
		if kind.V != formatVersion {
			return fmt.Errorf("unknown session format %d", kind.V)
		}
		var h headerRecord
		if err := json.Unmarshal(line, &h); err != nil {
			return err
		}
		s.ID, s.Created, s.Updated, s.Root, s.ProjectID, s.ParentID, s.ForkOf = h.ID, h.Created, h.Created, h.Root, h.ProjectID, h.ParentID, h.ForkOf
		if h.LegacyUsage != nil {
			s.legacy = *h.LegacyUsage
			s.Usage.Add(s.legacy)
		}
		s.legacyContext = h.LegacyContext
		return nil
	}
	switch kind.Type {
	case recEntry:
		var r entryRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		_, again := s.index[r.ID]
		s.put(r.Entry)
		if !again { // a later line for an entry replaces it in place
			s.Head = r.ID
			s.Updated = r.Time
			if r.Kind == KindMessage && r.Message != nil {
				s.titleFrom(*r.Message)
			}
		}
	case recLabel:
		var r labelRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		s.setLabel(r.Target, r.Text)
	case recBranchSummary:
		var r summaryRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		s.setBranchSummary(r.Target, BranchSummary{Text: r.Text, Model: r.Model, Usage: r.Usage})
		s.Usage.Add(r.Usage)
	case recState:
		var r stateRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		s.Head, s.Title, s.Mode, s.Provider, s.Model, s.Status, s.Updated = r.Head, r.Title, r.Mode, r.Provider, r.Model, r.Status, r.Updated
	default:
		// A record from a newer hm: skip it.
	}
	return nil
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

func (st *Store) file(id string) string { return filepath.Join(st.dir, filepath.Base(id)+".jsonl") }
func (st *Store) v1File(id string) string {
	return filepath.Join(st.dir, filepath.Base(id)+".json")
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
		s, err := decodeV1(data)
		if err != nil {
			continue
		}
		if s.Root == "" {
			s.Root = root
		}
		if err := st.Save(s); err != nil {
			return err
		}
		_ = os.Remove(path)
	}
	_ = os.Remove(old) // only when empty
	return nil
}

// Save writes what changed in s since the last save: the whole file the
// first time, new lines after.
func (st *Store) Save(s *Session) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	s.mu.Lock()
	whole := !s.file.written
	if !whole {
		// Gone or cut short behind our back: write it whole again.
		if fi, err := os.Stat(st.file(s.ID)); err != nil || fi.Size() < s.file.size {
			whole = true
		}
	}
	var recs []any
	if whole {
		recs = s.all()
	} else {
		recs = append([]any(nil), s.file.pending...)
		if s.file.state {
			recs = append(recs, s.stateRecord())
		}
	}
	taken := len(s.file.pending)
	s.file.state = false
	size, nl := s.file.size, s.file.nl
	recs, blobs := withoutImageData(recs)
	data, err := lines(recs)
	s.mu.Unlock()
	if err != nil || len(recs) == 0 {
		return err
	}
	if err := st.writeImages(blobs); err != nil {
		s.mu.Lock()
		s.file.state = true
		s.mu.Unlock()
		return err
	}

	err = st.write(s.ID, data, whole, size, nl)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.file.state = true // try the state again next time
		return err
	}
	s.file.pending = s.file.pending[taken:]
	s.file.written = true
	if whole {
		s.file.size = int64(len(data))
	} else {
		s.file.size += int64(len(data))
		if nl {
			s.file.size++
		}
	}
	s.file.nl = false
	return nil
}

// Pictures are kept out of the session file, which is read whole to list
// sessions: each goes in the images folder once, named by its hash, and
// the line keeps the hash. Sessions share the files; forks and branches
// point at the same ones.

// withoutImageData returns recs with the pictures' data taken out of the
// entries, and the pictures to write.
func withoutImageData(recs []any) ([]any, []provider.ImageData) {
	var blobs []provider.ImageData
	out, copied := recs, false
	for i, r := range recs {
		er, ok := r.(entryRecord)
		if !ok || er.Message == nil {
			continue
		}
		m, found := stripMessage(*er.Message, &blobs)
		if !found {
			continue
		}
		if !copied {
			out, copied = slices.Clone(recs), true
		}
		er.Message = &m
		out[i] = er
	}
	return out, blobs
}

// stripMessage copies m with its pictures' data left out, adding them to
// blobs.
func stripMessage(m provider.Message, blobs *[]provider.ImageData) (provider.Message, bool) {
	strip := func(im provider.ImageData) provider.ImageData {
		if im.Hash == "" {
			im.Hash = provider.HashOf(im.Data)
		}
		if len(im.Data) > 0 {
			*blobs = append(*blobs, im)
		}
		im.Data = nil
		return im
	}
	found := false
	var blocks []provider.Block
	for i, b := range m.Blocks {
		if b.Image == nil && len(b.Images) == 0 {
			continue
		}
		if !found {
			blocks = slices.Clone(m.Blocks)
			found = true
		}
		if b.Image != nil {
			im := strip(*b.Image)
			blocks[i].Image = &im
		}
		if len(b.Images) > 0 {
			ims := make([]provider.ImageData, len(b.Images))
			for k, im := range b.Images {
				ims[k] = strip(im)
			}
			blocks[i].Images = ims
		}
	}
	if found {
		m.Blocks = blocks
	}
	return m, found
}

func (st *Store) imageFile(im provider.ImageData) string {
	ext := strings.TrimPrefix(im.MediaType, "image/")
	if ext == "jpeg" {
		ext = "jpg"
	}
	return filepath.Join(st.dir, "images", filepath.Base(im.Hash)+"."+ext)
}

// writeImages puts each picture in its file, unless it is there.
func (st *Store) writeImages(ims []provider.ImageData) error {
	for _, im := range ims {
		path := st.imageFile(im)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, im.Data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	return nil
}

// imageGone stands for a picture whose file was deleted.
const imageGone = "[image no longer on disk]"

// readImages loads the pictures of s's entries from their files. One
// whose file is gone becomes a note saying so.
func (st *Store) readImages(s *Session) {
	load := func(im *provider.ImageData) bool {
		if len(im.Data) == 0 {
			im.Data, _ = os.ReadFile(st.imageFile(*im))
		}
		return len(im.Data) > 0
	}
	for _, e := range s.entries {
		if e.Message == nil {
			continue
		}
		for i := range e.Message.Blocks {
			b := &e.Message.Blocks[i]
			if b.Image != nil && !load(b.Image) {
				*b = provider.Block{Type: provider.Text, Text: imageGone}
			}
			kept := b.Images[:0]
			for k := range b.Images {
				if load(&b.Images[k]) {
					kept = append(kept, b.Images[k])
				} else {
					b.Text += "\n" + imageGone
				}
			}
			if len(b.Images) > 0 {
				b.Images = kept
			}
		}
	}
}

// write puts data in id's file: as the whole file, or after the good
// lines, which cuts off a line a crash left half written.
func (st *Store) write(id string, data []byte, whole bool, size int64, nl bool) error {
	if err := os.MkdirAll(st.dir, 0o700); err != nil {
		return err
	}
	path := st.file(id)
	if whole {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return err
	}
	if nl {
		data = append([]byte{'\n'}, data...)
	}
	if _, err := f.WriteAt(data, size); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Load reads a session. One in the old format is converted, and the old
// file kept in the v1 folder.
func (st *Store) Load(id string) (*Session, error) {
	data, err := os.ReadFile(st.file(id))
	if err == nil {
		s, err := decodeSession(data)
		if err == nil {
			st.readImages(s)
		}
		return s, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	old := st.v1File(id)
	data, err = os.ReadFile(old)
	if err != nil {
		return nil, err
	}
	s, err := decodeV1(data)
	if err != nil {
		return nil, err
	}
	if err := st.Save(s); err != nil {
		return nil, err
	}
	backup := filepath.Join(st.dir, "v1")
	if err := os.MkdirAll(backup, 0o700); err == nil {
		_ = os.Rename(old, filepath.Join(backup, filepath.Base(old)))
	}
	return s, nil
}

// Delete removes a session.
func (st *Store) Delete(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, p := range []string{st.file(id), st.v1File(id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Summary describes a saved session for the list.
type Summary struct {
	ID, Title, Mode, Provider string
	Root, ProjectID, ParentID string
	// ForkOf is the session this one was forked from.
	ForkOf  string
	Status  string
	Updated time.Time
	// Turns counts the user's messages on the active path.
	Turns int
	Cost  float64
}

// List returns saved sessions, newest first. Sessions in the old format
// are read as they are; they are converted when opened.
func (st *Store) List() []Summary {
	entries, _ := os.ReadDir(st.dir)
	seen := map[string]bool{}
	var out []Summary
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := strings.CutSuffix(e.Name(), ".jsonl")
		decode := decodeSession
		if !ok {
			if id, ok = strings.CutSuffix(e.Name(), ".json"); !ok {
				continue
			}
			decode = decodeV1
		}
		if seen[id] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(st.dir, e.Name()))
		if err != nil {
			continue
		}
		s, err := decode(data)
		if err != nil {
			continue
		}
		seen[id] = true
		out = append(out, s.summary())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

func (s *Session) summary() Summary {
	turns := 0
	for _, m := range s.Snapshot() {
		if PromptText(m) != "" {
			turns++
		}
	}
	sum := Summary{ID: s.ID, Title: s.Title, Mode: s.Mode, Provider: s.Provider,
		Root: s.Root, ProjectID: s.ProjectID, ParentID: s.ParentID, Status: s.Status,
		Updated: s.Updated, Turns: turns, Cost: s.Usage.Cost}
	if s.ForkOf != nil {
		sum.ForkOf = s.ForkOf.Session
	}
	return sum
}

// sessionV1 is the old session file: one JSON object rewritten on every
// save.
type sessionV1 struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Mode        string             `json:"mode"`
	Provider    string             `json:"provider"`
	Root        string             `json:"root,omitempty"`
	ProjectID   string             `json:"project,omitempty"`
	ParentID    string             `json:"parent,omitempty"`
	Status      string             `json:"status,omitempty"`
	Created     time.Time          `json:"created"`
	Updated     time.Time          `json:"updated"`
	Messages    []provider.Message `json:"messages"`
	Times       []time.Time        `json:"times,omitempty"`
	Usage       provider.Usage     `json:"usage"`
	Context     int64              `json:"context,omitempty"`
	Todos       []tools.Todo       `json:"todos,omitempty"`
	Compactions []compactionV1     `json:"compactions,omitempty"`
}

type compactionV1 struct {
	At           int       `json:"at"`
	Summary      string    `json:"summary"`
	TokensBefore int64     `json:"tokens_before"`
	TokensAfter  int64     `json:"tokens_after"`
	Model        string    `json:"model,omitempty"`
	Time         time.Time `json:"time"`
}

// decodeV1 turns an old session into a tree with one branch. Each
// compaction goes after the last message older than it.
func decodeV1(data []byte) (*Session, error) {
	var v sessionV1
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, errors.New("not a session")
	}
	s := &Session{ID: v.ID, Title: v.Title, Mode: v.Mode, Provider: v.Provider, Root: v.Root,
		ProjectID: v.ProjectID, ParentID: v.ParentID, Status: v.Status, Created: v.Created,
		legacy: v.Usage, legacyContext: v.Context}
	s.Usage = v.Usage
	ids := make([]string, len(v.Messages))
	comps := v.Compactions
	addComps := func(before time.Time, all bool) {
		for len(comps) > 0 && comps[0].At < len(ids) && ids[comps[0].At] != "" &&
			(all || !comps[0].Time.IsZero() && !before.IsZero() && comps[0].Time.Before(before)) {
			c := comps[0]
			comps = comps[1:]
			s.add(Entry{Kind: KindCompaction, Time: c.Time, Compaction: &Compaction{KeptFrom: ids[c.At],
				Summary: c.Summary, TokensBefore: c.TokensBefore, TokensAfter: c.TokensAfter, Model: c.Model, Time: c.Time}})
		}
	}
	for i := range v.Messages {
		var t time.Time
		if i < len(v.Times) {
			t = v.Times[i]
		}
		if i > 0 {
			addComps(t, false)
		}
		m := v.Messages[i]
		s.put(Entry{ID: s.newEntryID(), ParentID: s.Head, Kind: KindMessage, Time: t, Message: &m})
		s.Head = s.entries[len(s.entries)-1].ID
		ids[i] = s.Head
	}
	addComps(time.Time{}, true)
	if len(v.Todos) > 0 {
		s.add(Entry{Kind: KindTodos, Time: v.Updated, Todos: v.Todos})
	}
	s.pathOK = false
	s.settle()
	s.Updated = v.Updated
	s.file = fileState{}
	return s, nil
}
