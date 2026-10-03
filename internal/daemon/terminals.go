package daemon

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/store"
)

// terminalRegistry is the in-memory copy of the terminal-to-session map pane rows persist.
// Readers never touch SQLite; the store syncs it after every commit that can move a pane.
type terminalRegistry struct {
	mu    sync.RWMutex
	byID  map[harness.TerminalID]*terminal
	shown uint64
	// A terminal that leaves its pane can still exit, killed by its session's close or on its own;
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

// loadTerminals feeds the registry from the store, which hands it every pane row after each commit.
func (d *Daemon) loadTerminals() {
	d.store.OnPaneTerminals(d.terminals().sync)
}

// shownIn resolves a terminal to its session. A runtime no pane places is its own session
// when one is open under its id: runtimes from before the upgrade share their session's id.
func (d *Daemon) shownIn(t harness.TerminalID) (string, bool) {
	if s, ok := d.terminals().Showing(t); ok {
		return string(s), true
	}
	if t != "" && d.store != nil && d.store.Get(string(t)) != nil {
		return string(t), true
	}
	return "", false
}

// callerID resolves the id a hook or CLI call carries: the session a terminal shows, else the id
// unchanged, so an open session's id and an unknown id reach their handler as before.
func (d *Daemon) callerID(id string) string {
	if s, ok := d.terminals().Showing(harness.TerminalID(strings.TrimSpace(id))); ok {
		return string(s)
	}
	return id
}

// terminalsOf lists the terminals showing a session; a session no pane places runs under its own id.
func (d *Daemon) terminalsOf(sessionID string) []harness.TerminalID {
	if ids := d.terminals().Of(harness.SessionID(sessionID)); len(ids) > 0 {
		return ids
	}
	return []harness.TerminalID{harness.TerminalID(sessionID)}
}

// primaryTerminal prefers a live terminal; the backend is asked only when several show the session.
func (d *Daemon) primaryTerminal(sessionID string) harness.TerminalID {
	ids := d.terminalsOf(sessionID)
	if len(ids) > 1 {
		live := d.liveTerminals(context.Background())
		for _, id := range ids {
			if _, ok := live[id]; ok {
				return id
			}
		}
	}
	return ids[0]
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

func (d *Daemon) sessionLive(ctx context.Context, sessionID string) bool {
	live := d.liveTerminals(ctx)
	for _, id := range d.terminalsOf(sessionID) {
		if _, ok := live[id]; ok {
			return true
		}
	}
	return false
}

func (d *Daemon) liveSessions(ctx context.Context) map[string]struct{} {
	sessions := make(map[string]struct{})
	for id := range d.liveTerminals(ctx) {
		if sessionID, ok := d.shownIn(id); ok {
			sessions[sessionID] = struct{}{}
		}
	}
	return sessions
}

// sync replaces the registry's placements with panes; a terminal no pane holds any more is ending.
func (r *terminalRegistry) sync(panes []store.PaneTerminal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	placed := make(map[harness.TerminalID]struct{}, len(panes))
	for _, pane := range panes {
		id := harness.TerminalID(strings.TrimSpace(pane.RuntimeID))
		session := harness.SessionID(strings.TrimSpace(pane.SessionID))
		if id == "" || session == "" {
			continue
		}
		placed[id] = struct{}{}
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
		if _, kept := placed[id]; !kept {
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
