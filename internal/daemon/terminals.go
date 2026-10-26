package daemon

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type terminalRegistry struct {
	mu       sync.RWMutex
	byID     map[harness.TerminalID]*terminal
	titles   map[harness.TerminalID]string
	shown    uint64
	ending   map[harness.TerminalID]endingTerminal
	expected map[harness.TerminalID]expectedTerminal
}

type terminal struct {
	shows    harness.SessionID
	copied   bool
	shownSeq uint64
	lastKey  time.Time
}

func (d *Daemon) terminals() *terminalRegistry {
	d.terminalsOnce.Do(func() {
		d.terminalState = &terminalRegistry{byID: make(map[harness.TerminalID]*terminal), titles: make(map[harness.TerminalID]string), ending: make(map[harness.TerminalID]endingTerminal), expected: make(map[harness.TerminalID]expectedTerminal)}
	})
	return d.terminalState
}

func (d *Daemon) loadTerminals() {
	d.store.OnTerminalBindings(func(bindings []store.TerminalBinding) {
		d.terminals().sync(bindings)
		d.kickMonitorTerminals()
	})
}

func (d *Daemon) shownIn(t harness.TerminalID) (protocol.SessionID, bool) {
	return d.terminals().Showing(t)
}

func (d *Daemon) sessionInTerminal(t protocol.TerminalID) protocol.SessionID {
	if session, _, ours := d.linkCaller(t); ours {
		return session
	}
	session, _ := d.terminals().Showing(protocol.TrimID(t))
	return session
}

func (d *Daemon) terminalsOf(sessionID protocol.SessionID) []harness.TerminalID {
	return d.terminals().Of(sessionID)
}

func (d *Daemon) primaryTerminal(sessionID protocol.SessionID) harness.TerminalID {
	ids := d.terminalsOf(sessionID)
	if len(ids) > 1 {
		live := d.liveTerminals(context.Background())
		for _, id := range ids {
			if _, ok := live[id]; ok {
				return id
			}
		}
	}
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func (d *Daemon) liveTerminals(ctx context.Context) map[harness.TerminalID]struct{} {
	live := make(map[harness.TerminalID]struct{})
	if d.ptyBackend == nil {
		return live
	}
	for _, id := range d.ptyBackend.TerminalIDs(ctx) {
		live[id] = struct{}{}
	}
	return live
}

func (d *Daemon) terminalLive(t harness.TerminalID) bool {
	_, live := d.liveTerminals(context.Background())[t]
	return live
}

func (d *Daemon) sessionLive(ctx context.Context, sessionID protocol.SessionID) bool {
	if d.sessionHost(sessionID) != "" {
		view, ok := d.monitorViewFor(sessionID)
		return ok && view.Live.V
	}
	live := d.liveTerminals(ctx)
	for _, id := range d.terminalsOf(sessionID) {
		if _, ok := live[id]; ok {
			return true
		}
	}
	return false
}

func (d *Daemon) liveSessions(ctx context.Context) map[protocol.SessionID]struct{} {
	sessions := make(map[protocol.SessionID]struct{})
	for id := range d.liveTerminals(ctx) {
		if sessionID, ok := d.shownIn(id); ok {
			sessions[sessionID] = struct{}{}
		}
	}
	return sessions
}

func (r *terminalRegistry) sync(bindings []store.TerminalBinding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	bound := make(map[harness.TerminalID]struct{}, len(bindings))
	for _, binding := range bindings {
		id := protocol.TrimID(binding.TerminalID)
		session := protocol.TrimID(binding.SessionID)
		if id == "" || session == "" {
			continue
		}
		bound[id] = struct{}{}
		delete(r.expected, id)
		entry := r.byID[id]
		if entry == nil {
			entry = &terminal{}
			r.byID[id] = entry
		}
		if entry.shows != session {
			r.shown++
			entry.shows, entry.shownSeq = session, r.shown
		}
		entry.copied = binding.Copied
	}
	now := time.Now()
	for id, entry := range r.byID {
		if _, kept := bound[id]; !kept {
			delete(r.byID, id)
			r.endLocked(id, endingTerminal{session: entry.shows, copied: entry.copied, at: now})
		}
	}
}

func (r *terminalRegistry) Showing(t harness.TerminalID) (harness.SessionID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry := r.byID[t]
	if entry == nil {
		expected, ok := r.expected[t]
		return expected.session, ok
	}
	return entry.shows, true
}

type expectedTerminal struct {
	session harness.SessionID
	copied  bool
}

func (r *terminalRegistry) expect(t harness.TerminalID, s harness.SessionID, copied bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expected[t] = expectedTerminal{session: s, copied: copied}
}

func (r *terminalRegistry) unexpect(t harness.TerminalID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.expected, t)
}

func (r *terminalRegistry) Of(s harness.SessionID) []harness.TerminalID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []harness.TerminalID
	for id, entry := range r.byID {
		if entry.shows == s {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b harness.TerminalID) int {
		return cmp.Compare(r.byID[b].shownSeq, r.byID[a].shownSeq)
	})
	for id, expected := range r.expected {
		if expected.session == s {
			ids = append(ids, id)
		}
	}
	return ids
}

func (r *terminalRegistry) Primary(s harness.SessionID) (harness.TerminalID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var primary harness.TerminalID
	var seq uint64
	for id, entry := range r.byID {
		if entry.shows == s && entry.shownSeq > seq {
			primary, seq = id, entry.shownSeq
		}
	}
	return primary, seq > 0
}

func (r *terminalRegistry) noteKey(t harness.TerminalID, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.byID[t]; entry != nil {
		entry.lastKey = at
	}
}

func (r *terminalRegistry) lastKeyOf(t harness.TerminalID) time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry := r.byID[t]; entry != nil {
		return entry.lastKey
	}
	return time.Time{}
}

const endingGrace = time.Minute

type endingTerminal struct {
	session harness.SessionID
	copied  bool
	at      time.Time
}

func (r *terminalRegistry) noteEnding(t harness.TerminalID, s harness.SessionID, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	note := endingTerminal{session: s, at: now}
	if entry := r.byID[t]; entry != nil && entry.shows == s {
		note.copied = entry.copied
	}
	if old, ok := r.ending[t]; ok && old.session == s {
		note.copied = note.copied || old.copied
	}
	r.endLocked(t, note)
}

func (r *terminalRegistry) endLocked(t harness.TerminalID, note endingTerminal) {
	for id, old := range r.ending {
		if note.at.Sub(old.at) > endingGrace {
			delete(r.ending, id)
		}
	}
	r.ending[t] = note
}

func (r *terminalRegistry) takeEnding(t harness.TerminalID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ending, t)
}

func (r *terminalRegistry) takeExiting(t harness.TerminalID) (owner endingTerminal, bound, shown bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	note, ending := r.ending[t]
	delete(r.ending, t)
	if entry := r.byID[t]; entry != nil {
		return endingTerminal{session: entry.shows, copied: entry.copied}, true, true
	}
	if expected, ok := r.expected[t]; ok {
		return endingTerminal{session: expected.session, copied: expected.copied}, true, true
	}
	return note, false, ending
}

func (r *terminalRegistry) Bindings() []protocol.TerminalBinding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bindings := make([]protocol.TerminalBinding, 0, len(r.byID))
	for id, entry := range r.byID {
		bindings = append(bindings, protocol.TerminalBinding{TerminalID: id, SessionID: entry.shows})
	}
	slices.SortFunc(bindings, func(a, b protocol.TerminalBinding) int { return cmp.Compare(a.TerminalID, b.TerminalID) })
	return bindings
}

func (d *Daemon) projectTerminalBindings() {
	d.projectSnapshot(protocol.EventTerminalBindingsUpdated, func() {
		d.wsHub.BroadcastValue(&protocol.TerminalBindingsUpdatedMessage{Event: protocol.EventTerminalBindingsUpdated, TerminalBindings: d.terminals().Bindings()})
	})
}

func (r *terminalRegistry) owner(t harness.TerminalID) protocol.SessionID {
	if id, ok := r.Showing(t); ok {
		return id
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ending[t].session
}
