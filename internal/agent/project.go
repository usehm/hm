package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Project is context shared by the sessions started in it: what the work
// is about, told to the agent before every task.
type Project struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Context     string    `json:"context,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

// Projects keeps every project in one file; projects are not tied to a
// workspace.
type Projects struct {
	path string
	mu   sync.Mutex
	list []Project
}

// LoadProjects reads the projects under state. A missing file is no
// projects; a broken one is an error, and saving would overwrite it.
func LoadProjects(state string) (*Projects, error) {
	ps := &Projects{path: filepath.Join(state, "projects.json")}
	data, err := os.ReadFile(ps.path)
	if errors.Is(err, os.ErrNotExist) {
		return ps, nil
	}
	if err != nil {
		return ps, err
	}
	return ps, json.Unmarshal(data, &ps.list)
}

// List returns the projects, most recently changed first.
func (ps *Projects) List() []Project {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := append([]Project(nil), ps.list...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Get returns the project with id.
func (ps *Projects) Get(id string) (Project, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, p := range ps.list {
		if p.ID == id {
			return p, true
		}
	}
	return Project{}, false
}

// Put adds p, or replaces the project with its ID, and saves. A new
// project gets an ID; the stored project is returned.
func (ps *Projects) Put(p Project) (Project, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	t := now()
	p.Title = strings.TrimSpace(p.Title)
	p.Updated = t
	if p.ID == "" {
		var b [6]byte
		_, _ = rand.Read(b[:])
		p.ID = hex.EncodeToString(b[:])
		p.Created = t
		ps.list = append(ps.list, p)
		return p, ps.save()
	}
	for i := range ps.list {
		if ps.list[i].ID == p.ID {
			p.Created = ps.list[i].Created
			ps.list[i] = p
			return p, ps.save()
		}
	}
	if p.Created.IsZero() {
		p.Created = t
	}
	ps.list = append(ps.list, p)
	return p, ps.save()
}

// Delete removes the project with id and saves.
func (ps *Projects) Delete(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i := range ps.list {
		if ps.list[i].ID == id {
			ps.list = append(ps.list[:i], ps.list[i+1:]...)
			return ps.save()
		}
	}
	return nil
}

func (ps *Projects) save() error {
	data, err := json.MarshalIndent(ps.list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ps.path), 0o700); err != nil {
		return err
	}
	tmp := ps.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ps.path)
}

// Prompt is the project's part of the system prompt; empty when the project
// says nothing.
func (p *Project) Prompt() string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	if p.Title != "" {
		b.WriteString("Project: " + p.Title + "\n")
	}
	if d := strings.TrimSpace(p.Description); d != "" {
		b.WriteString(d + "\n")
	}
	if c := strings.TrimSpace(p.Context); c != "" {
		b.WriteString("\n" + c + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "# Project context\nThe user keeps this context for the project the task belongs to. Treat it as background, as you would notes from the user.\n\n" + b.String()
}
