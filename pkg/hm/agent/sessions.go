// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/usehm/hm/internal/agent"
	"github.com/usehm/hm/internal/agent/export"
	"github.com/usehm/hm/internal/agent/host"
	"github.com/usehm/hm/internal/config"
)

// SessionSummary describes a saved session.
type SessionSummary struct {
	ID, Title, Mode, Provider string
	Root                      string
	ParentID                  string // the session that started it as a sub-agent
	ForkOf                    string // the session it was forked from
	Status                    string
	Updated                   time.Time
	Turns                     int // the user's messages on the active path
	Cost                      float64
}

// Sessions lists the saved sessions, newest first: those of the Runner's
// workspace, or every one with all.
func (r *Runner) Sessions(all bool) []SessionSummary {
	st := r.store
	if st == nil {
		st = agent.NewStore(config.StateDir())
	}
	var out []SessionSummary
	for _, s := range st.List() {
		if !all && s.Root != r.h.Root {
			continue
		}
		out = append(out, SessionSummary{ID: s.ID, Title: s.Title, Mode: s.Mode, Provider: s.Provider, Root: s.Root,
			ParentID: s.ParentID, ForkOf: s.ForkOf, Status: s.Status, Updated: s.Updated, Turns: s.Turns, Cost: s.Cost})
	}
	return out
}

// Fork goes on in a new session that starts from entry of this one, or
// from its latest message when entry is empty. The Runner works in the
// fork from then on; the old session stays as it was.
func (r *Runner) Fork(entry string) (SessionInfo, error) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return SessionInfo{}, ErrBusy
	}
	r.running = true // no turn starts meanwhile
	r.mu.Unlock()
	err := r.fork(entry)
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
	if err != nil {
		return SessionInfo{}, err
	}
	return r.Session(), nil
}

// fork switches r to a fork of its session at entry.
func (r *Runner) fork(entry string) error {
	old := r.session
	if entry == "" {
		ids := old.MessageIDs()
		if len(ids) == 0 {
			return errors.New("nothing to fork yet")
		}
		entry = ids[len(ids)-1]
	}
	f, err := old.Fork(entry)
	if err != nil {
		return fmt.Errorf("fork %s: %w", entry, err)
	}
	r.save()
	r.bus.Unregister(old.ID)
	r.shells.StopAll()
	r.init(f)
	r.resumed = false
	r.save()
	return nil
}

// ExportFormat is a file format Export writes.
type ExportFormat string

const (
	ExportHTML     ExportFormat = "html"
	ExportMarkdown ExportFormat = "md"
	ExportJSONL    ExportFormat = "jsonl" // the raw session
)

// Export renders the session as a file of format: the active path, or
// every branch with allBranches. Secrets hm knows of and values shaped
// like keys are taken out first; redacted says what went, such as
// "2 API keys".
func (r *Runner) Export(format ExportFormat, allBranches bool) (data []byte, redacted []string, err error) {
	f, ok := export.ParseFormat(string(format))
	if !ok {
		return nil, nil, fmt.Errorf("unknown export format %q; use html, md or jsonl", format)
	}
	res, err := export.Render(r.session, export.Options{Format: f, AllBranches: allBranches,
		Redactor: export.NewRedactor(host.KnownSecrets(r.h.Config)...)})
	if err != nil {
		return nil, nil, err
	}
	for _, x := range res.Redacted {
		redacted = append(redacted, fmt.Sprintf("%d %s", x.Count, x.Kind))
	}
	return res.Data, redacted, nil
}

// ExportPath is where an export of the session goes: p as given, relative
// to the workspace, with the default file name when p is a folder; the
// Downloads folder when p is empty.
func (r *Runner) ExportPath(p string, format ExportFormat) string {
	f, _ := export.ParseFormat(string(format))
	home, _ := os.UserHomeDir()
	title, _, _ := r.session.Meta()
	title, _ = export.NewRedactor(host.KnownSecrets(r.h.Config)...).Redact(title)
	return export.Path(p, f, title, time.Now(), r.h.Root, home)
}
