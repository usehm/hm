package agent

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mail is a message between agents.
type Mail struct {
	From, FromName, Text string
}

// Bus carries messages between the agents running in this window. Each
// agent has a mailbox by its session ID; messages wait there until the
// agent takes them between steps or waits for one.
type Bus struct {
	mu     sync.Mutex
	boxes  map[string]*mailbox
	notify func(id string)
}

type mailbox struct {
	name, parent string
	running      bool
	mail         []Mail
	wake         chan struct{} // closed and replaced when mail arrives
}

// NewBus returns an empty bus; notify, if set, is called (on the sender's
// goroutine) when mail arrives for id.
func NewBus(notify func(id string)) *Bus {
	return &Bus{boxes: map[string]*mailbox{}, notify: notify}
}

// Register opens id's mailbox, keeping mail already in it.
func (b *Bus) Register(id, name, parent string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if m, ok := b.boxes[id]; ok {
		m.name, m.parent = name, parent
		return
	}
	b.boxes[id] = &mailbox{name: name, parent: parent, wake: make(chan struct{})}
}

// Unregister closes id's mailbox; mail sent to it is refused after.
func (b *Bus) Unregister(id string) {
	b.mu.Lock()
	delete(b.boxes, id)
	b.mu.Unlock()
}

// SetRunning records whether id is working, for Peers.
func (b *Bus) SetRunning(id string, v bool) {
	b.mu.Lock()
	if m, ok := b.boxes[id]; ok {
		m.running = v
	}
	b.mu.Unlock()
}

// resolve finds the mailbox to names, from the point of view of from:
// "parent", an ID, or a name (a child's first).
func (b *Bus) resolve(from, to string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "parent" {
		if m, ok := b.boxes[from]; ok && m.parent != "" {
			if _, ok := b.boxes[m.parent]; ok {
				return m.parent, nil
			}
		}
		return "", errors.New("no parent agent is open")
	}
	if _, ok := b.boxes[to]; ok {
		return to, nil
	}
	var match []string
	for id, m := range b.boxes {
		if id != from && strings.EqualFold(m.name, to) {
			if m.parent == from {
				return id, nil
			}
			match = append(match, id)
		}
	}
	switch len(match) {
	case 1:
		return match[0], nil
	case 0:
		return "", errors.New("no agent named " + to + "; list_agents shows who is there")
	}
	return "", errors.New("several agents are named " + to + "; use an ID from list_agents")
}

// Send puts text in to's mailbox.
func (b *Bus) Send(from, to, text string) error {
	b.mu.Lock()
	id, err := b.resolve(from, to)
	if err != nil {
		b.mu.Unlock()
		return err
	}
	if id == from {
		b.mu.Unlock()
		return errors.New("that is you")
	}
	name := ""
	if m, ok := b.boxes[from]; ok {
		name = m.name
	}
	m := b.boxes[id]
	m.mail = append(m.mail, Mail{From: from, FromName: name, Text: text})
	close(m.wake)
	m.wake = make(chan struct{})
	b.mu.Unlock()
	if b.notify != nil {
		b.notify(id)
	}
	return nil
}

// Take returns and removes id's mail.
func (b *Bus) Take(id string) []Mail {
	b.mu.Lock()
	defer b.mu.Unlock()
	m, ok := b.boxes[id]
	if !ok {
		return nil
	}
	out := m.mail
	m.mail = nil
	return out
}

// Pending reports whether id has mail.
func (b *Bus) Pending(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	m, ok := b.boxes[id]
	return ok && len(m.mail) > 0
}

// Wait takes the next message for id, from anyone when from is empty,
// waiting up to timeout.
func (b *Bus) Wait(ctx context.Context, id, from string, timeout time.Duration) (Mail, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		b.mu.Lock()
		m, ok := b.boxes[id]
		if !ok {
			b.mu.Unlock()
			return Mail{}, false
		}
		want := ""
		if from != "" {
			want, _ = b.resolve(id, from)
		}
		for i, mail := range m.mail {
			if from == "" || mail.From == want || strings.EqualFold(mail.FromName, from) {
				m.mail = append(m.mail[:i], m.mail[i+1:]...)
				b.mu.Unlock()
				return mail, true
			}
		}
		wake := m.wake
		b.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return Mail{}, false
		case <-deadline.C:
			return Mail{}, false
		}
	}
}

// Peers lists the mailboxes other than id's, as id sees them.
func (b *Bus) Peers(id string) []PeerInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	me := b.boxes[id]
	var out []PeerInfo
	for oid, m := range b.boxes {
		if oid == id {
			continue
		}
		role := "sibling"
		switch {
		case me != nil && me.parent == oid:
			role = "parent"
		case m.parent == id:
			role = "child"
		case me != nil && me.parent == "" && m.parent == "":
			role = "peer"
		}
		out = append(out, PeerInfo{ID: oid, Name: m.name, Role: role, Running: m.running})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PeerInfo is a mailbox as Peers lists it.
type PeerInfo struct {
	ID, Name, Role string
	Running        bool
}
