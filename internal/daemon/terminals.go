package daemon

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// terminalRegistry is the in-memory copy of the terminal-to-session map pane rows persist.
// Readers never touch SQLite; pane rows reach it only through the layout writers below.
type terminalRegistry struct {
	write sync.Mutex
	mu    sync.RWMutex
	byID  map[harness.TerminalID]*terminal
	shown uint64
	// A session's close kills its terminals after their panes are gone; their exits still name it.
	ending map[harness.TerminalID]endingTerminal
}

type terminal struct {
	shows     harness.SessionID
	workspace string
	shownSeq  uint64
	lastKey   time.Time
}

func (d *Daemon) terminals() *terminalRegistry {
	d.terminalsOnce.Do(func() {
		d.terminalState = &terminalRegistry{byID: make(map[harness.TerminalID]*terminal), ending: make(map[harness.TerminalID]endingTerminal)}
	})
	return d.terminalState
}

// saveWorkspaceLayout is the one writer of a layout's pane rows, so the registry follows every save.
func (d *Daemon) saveWorkspaceLayout(snapshot workspacelayout.WorkspaceLayout) error {
	r := d.terminals()
	r.write.Lock()
	defer r.write.Unlock()
	if err := d.store.SaveWorkspaceLayout(snapshot); err != nil {
		return err
	}
	r.place(snapshot.WorkspaceID, snapshot.Panes)
	return nil
}

func (d *Daemon) removeWorkspaceLayout(workspaceID string) {
	r := d.terminals()
	r.write.Lock()
	defer r.write.Unlock()
	d.store.RemoveWorkspaceLayout(workspaceID)
	r.unplaceWorkspace(workspaceID)
}

func (d *Daemon) removeWorkspaceRecord(workspaceID string) {
	r := d.terminals()
	r.write.Lock()
	defer r.write.Unlock()
	d.store.RemoveWorkspace(workspaceID)
	r.unplaceWorkspace(workspaceID)
}

func (d *Daemon) loadTerminals() {
	r := d.terminals()
	r.write.Lock()
	defer r.write.Unlock()
	for _, workspaceID := range d.store.WorkspaceLayoutIDs() {
		if layout := d.store.GetWorkspaceLayout(workspaceID); layout != nil {
			r.place(workspaceID, layout.Panes)
		}
	}
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

func (r *terminalRegistry) place(workspaceID string, panes []workspacelayout.Pane) {
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
		entry := r.byID[id]
		if entry == nil {
			entry = &terminal{}
			r.byID[id] = entry
		}
		if entry.shows != session {
			r.shown++
			entry.shows, entry.shownSeq = session, r.shown
		}
		entry.workspace = workspaceID
	}
	for id, entry := range r.byID {
		if _, kept := placed[id]; !kept && entry.workspace == workspaceID {
			delete(r.byID, id)
		}
	}
}

func (r *terminalRegistry) unplaceWorkspace(workspaceID string) {
	r.place(workspaceID, nil)
}

func (r *terminalRegistry) Showing(t harness.TerminalID) (harness.SessionID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry := r.byID[t]
	if entry == nil {
		return "", false
	}
	return entry.shows, true
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

// An exit owed to a teardown lands within milliseconds (embedded) or never (worker),
// so a minute bounds the notes without racing a late exit.
const endingGrace = time.Minute

type endingTerminal struct {
	session harness.SessionID
	at      time.Time
}

func (r *terminalRegistry) noteEnding(t harness.TerminalID, s harness.SessionID, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
