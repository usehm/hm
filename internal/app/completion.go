package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/agent/complete"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/config"
)

// complState follows the active editor and asks for an inline completion
// once typing pauses.
type complState struct {
	path    string
	rev     uint64
	caret   int
	at      time.Time // when the last edit was seen
	pending bool      // an edit is waiting for the pause
	force   bool      // the next request was asked for by hand
	cancel  context.CancelFunc
	seq     uint64 // request id; replies to older ones are dropped
	errText string // last error shown, so it is not repeated per keystroke

	// prov is the provider built for provKey, kept so the keychain is not
	// read on every request.
	mu      sync.Mutex
	prov    provider.Provider
	provKey string
}

// completionProvider is the agent provider completions use.
func (a *App) completionProvider() string {
	cfg := a.cfg.Current()
	if p := cfg.Completion.Provider; p != "" {
		return p
	}
	return cfg.Agent.DefaultProvider
}

// completionReady reports whether completions can be asked for.
func (a *App) completionReady() bool {
	name := a.completionProvider()
	_, ok := a.cfg.Current().Agent.Providers[name]
	return ok && a.cfg.Current().Completion.Model != "" && a.providerReady(name)
}

// stopRequest cancels the request in flight and drops its reply.
func (st *complState) stopRequest() {
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	st.seq++
}

// pumpCompletion runs each frame: it notes edits in the focused editor and
// asks for a completion when typing has paused at a place that fits one.
// Moving the caret alone never asks.
func (a *App) pumpCompletion(c *ui.Ctx) {
	st := &a.compl
	cc := a.cfg.Current().Completion
	ed := a.ws.ActiveEditor()
	if ed == nil || !ed.Focused() || ed.ReadOnly() {
		if st.cancel != nil {
			st.stopRequest()
		}
		st.pending, st.force = false, false
		return
	}
	rev, caret := ed.Revision(), ed.Caret()
	if ed.Path != st.path || rev != st.rev || caret != st.caret {
		edited := ed.Path == st.path && rev != st.rev
		st.path, st.rev, st.caret, st.at = ed.Path, rev, caret, time.Now()
		st.stopRequest()
		// Typing through a suggestion keeps the rest of it; no new request.
		st.pending = edited && ed.InlineSuggestion() == ""
	}
	if st.force {
		st.force, st.pending = false, false
		a.requestCompletion(ed, true)
		return
	}
	if !st.pending || !cc.Enabled || !a.completionReady() {
		return
	}
	if wait := cc.Delay.Duration - time.Since(st.at); wait > 0 {
		c.Animate(wait)
		return
	}
	st.pending = false
	a.requestCompletion(ed, false)
}

// triggerCompletion asks for a completion at the caret now.
func (a *App) triggerCompletion() {
	if !a.completionReady() {
		a.toast("Pick a completion model in Settings → Completion", ui.ToastInfo)
		return
	}
	a.compl.force = true
	a.invalidate()
}

// requestCompletion asks the model about the caret in ed and shows the
// reply as ghost text if ed has not changed meanwhile. Unasked requests
// only run where complete.Eligible allows.
func (a *App) requestCompletion(ed *ui.Editor, asked bool) {
	if ed.HasSelection() {
		return
	}
	text, caret := ed.Bytes(), ed.Caret()
	if !asked && !complete.Eligible(text, caret) {
		return
	}
	st := &a.compl
	cfg := a.cfg.Current()
	cc := cfg.Completion
	name := a.completionProvider()
	pc := cfg.Agent.Providers[name]
	st.stopRequest()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	st.cancel = cancel
	seq, path, rev := st.seq, ed.Path, ed.Revision()
	req := complete.Request{
		Path: a.ws.Rel(path), Text: text, Caret: caret,
		Model: cc.Model, MaxTokens: cc.MaxTokens,
	}
	go func() {
		defer cancel()
		var out string
		p, err := st.provider(name, pc)
		if err == nil {
			out, err = complete.Ask(ctx, p, req)
		}
		if ctx.Err() == context.Canceled {
			return
		}
		a.onUI(func() {
			if seq != st.seq {
				return
			}
			st.cancel = nil
			if err != nil {
				if msg := err.Error(); msg != st.errText {
					st.errText = msg
					a.toast("AI completion: "+msg, ui.ToastError)
				}
				return
			}
			st.errText = ""
			if out == "" && asked {
				a.toast("No completion here", ui.ToastInfo)
			}
			if e := a.bufs.Get(path); e != nil && e.Revision() == rev && e.Caret() == caret && e.Focused() {
				e.SetInlineSuggestion(out)
			}
		})
	}()
}

// provider returns the cached provider for name, building it when its
// config changed. It reads the keychain, so it runs off the UI goroutine.
func (st *complState) provider(name string, pc config.Provider) (provider.Provider, error) {
	key := fmt.Sprint(name, pc)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.prov != nil && st.provKey == key {
		return st.prov, nil
	}
	p, err := buildProvider(name, pc)
	if err != nil {
		return nil, err
	}
	st.prov, st.provKey = p, key
	return p, nil
}

// forgetProvider drops the cached provider, after a key changes.
func (st *complState) forgetProvider() {
	st.mu.Lock()
	st.prov = nil
	st.mu.Unlock()
}
