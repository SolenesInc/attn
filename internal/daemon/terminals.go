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

// terminalRegistry is the in-memory copy of the terminal-to-session map the store persists.
// Readers never touch SQLite; the store syncs it after every commit that can change a binding.
type terminalRegistry struct {
	mu    sync.RWMutex
	byID  map[harness.TerminalID]*terminal
	shown uint64
	// A closed session's terminal can still exit after its binding is removed;
	// that exit still names the session it showed.
	ending map[harness.TerminalID]endingTerminal
	// A launch's terminal starts before its pane exists; its hooks still name its session.
	expected map[harness.TerminalID]harness.SessionID
}

type terminal struct {
	shows    harness.SessionID
	shownSeq uint64
	lastKey  time.Time
}

func (d *Daemon) terminals() *terminalRegistry {
	d.terminalsOnce.Do(func() {
		d.terminalState = &terminalRegistry{byID: make(map[harness.TerminalID]*terminal), ending: make(map[harness.TerminalID]endingTerminal), expected: make(map[harness.TerminalID]harness.SessionID)}
	})
	return d.terminalState
}

// loadTerminals feeds the registry from persisted bindings after each commit.
func (d *Daemon) loadTerminals() {
	d.store.OnTerminalBindings(d.terminals().sync)
}

// shownIn resolves a terminal through its persisted or pending binding.
func (d *Daemon) shownIn(t harness.TerminalID) (protocol.SessionID, bool) {
	return d.terminals().Showing(t)
}

func (d *Daemon) sessionInTerminal(t protocol.TerminalID) protocol.SessionID {
	session, _ := d.terminals().Showing(protocol.TrimID(t))
	return session
}

func (d *Daemon) terminalsOf(sessionID protocol.SessionID) []harness.TerminalID {
	return d.terminals().Of(sessionID)
}

// primaryTerminal prefers a live terminal; the backend is asked only when several show the session.
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

// sync replaces the registry's bindings; a terminal whose session closed is ending.
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
	}
	now := time.Now()
	for id, entry := range r.byID {
		if _, kept := bound[id]; !kept {
			delete(r.byID, id)
			r.endLocked(id, entry.shows, now)
		}
	}
}

func (r *terminalRegistry) Showing(t harness.TerminalID) (harness.SessionID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry := r.byID[t]
	if entry == nil {
		s, ok := r.expected[t]
		return s, ok
	}
	return entry.shows, true
}

func (r *terminalRegistry) expect(t harness.TerminalID, s harness.SessionID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expected[t] = s
}

// unexpect drops a launch's terminal whose pane never came.
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
	for id, shows := range r.expected {
		if shows == s {
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

func (r *terminalRegistry) noteKey(t harness.TerminalID, at time.Time) (previous time.Time, placed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.byID[t]
	if entry == nil {
		return time.Time{}, false
	}
	previous, entry.lastKey = entry.lastKey, at
	return previous, true
}

func (r *terminalRegistry) lastKeyOf(t harness.TerminalID) time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry := r.byID[t]; entry != nil {
		return entry.lastKey
	}
	return time.Time{}
}

func (r *terminalRegistry) forgetKey(t harness.TerminalID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.byID[t]; entry != nil {
		entry.lastKey = time.Time{}
	}
}

// An exit owed to a closed pane lands within milliseconds (embedded) or never (worker),
// so a minute bounds the notes without racing a late exit.
const endingGrace = time.Minute

type endingTerminal struct {
	session harness.SessionID
	at      time.Time
}

func (r *terminalRegistry) noteEnding(t harness.TerminalID, s harness.SessionID, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endLocked(t, s, now)
}

func (r *terminalRegistry) endLocked(t harness.TerminalID, s harness.SessionID, now time.Time) {
	for id, note := range r.ending {
		if now.Sub(note.at) > endingGrace {
			delete(r.ending, id)
		}
	}
	r.ending[t] = endingTerminal{session: s, at: now}
}

func (r *terminalRegistry) takeEnding(t harness.TerminalID) (harness.SessionID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	note, ok := r.ending[t]
	delete(r.ending, t)
	return note.session, ok
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
