package daemon

import (
	"sync"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/statetrace"
)

type sessionMonitor struct {
	mu         sync.Mutex
	terminalID harness.TerminalID
	terminal   sessionstate.TerminalEvidence
	entries    map[*monitorEntry]struct{}
}

type monitorEntry struct {
	mu              sync.Mutex
	monitor         *sessionMonitor
	conversation    string
	evidence        sessionstate.ConversationEvidence
	resolveAt       time.Time
	resolvePending  bool
	dwell           dwellPending
	reason          string
	trace           *statetrace.Ring
	classifiedTurn  string
	classifyingTurn string
	forcedStop      time.Time
	silence         *time.Timer
}

type monitorEvidence struct {
	sessionstate.TerminalEvidence
	sessionstate.ConversationEvidence
}

func (e *monitorEvidence) ClearProgramStatus() {
	sessionstate.ClearProgramStatus(&e.TerminalEvidence, &e.ConversationEvidence)
}

type monitorRegistry struct {
	mu       sync.Mutex
	physical map[harness.TerminalID]*sessionMonitor
	sessions map[protocol.SessionID]*monitorEntry
	wake     chan struct{}
	closed   bool
}

func (d *Daemon) monitors() *monitorRegistry {
	d.monitorsOnce.Do(func() {
		d.monitorState = &monitorRegistry{physical: make(map[harness.TerminalID]*sessionMonitor), sessions: make(map[protocol.SessionID]*monitorEntry), wake: make(chan struct{}, 1)}
	})
	return d.monitorState
}

func (r *monitorRegistry) entry(sessionID protocol.SessionID) *monitorEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[sessionID]
}

func (d *Daemon) monitorEntry(sessionID protocol.SessionID) *monitorEntry {
	return d.monitors().entry(sessionID)
}

func (r *monitorRegistry) start(terminal harness.TerminalID, conversation string, sessionID protocol.SessionID, linked bool) *monitorEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.sessions[sessionID]
	if old != nil && old.monitor.terminalID == terminal {
		if (conversation == "" && !linked) || old.conversation == conversation {
			return old
		}
		if old.conversation == "" && conversation != "" {
			old.mu.Lock()
			defer old.mu.Unlock()
			old.conversation = conversation
			return old
		}
	}
	var m *sessionMonitor
	if !linked {
		m = r.physical[terminal]
	}
	if m == nil {
		m = &sessionMonitor{terminalID: terminal, entries: make(map[*monitorEntry]struct{})}
	}
	r.forgetLocked(sessionID)
	if !linked {
		r.physical[terminal] = m
	}
	e := &monitorEntry{monitor: m, conversation: conversation}
	m.entries[e] = struct{}{}
	r.sessions[sessionID] = e
	return e
}

func (d *Daemon) startSessionMonitor(sessionID protocol.SessionID) *monitorEntry {
	terminal := d.primaryTerminal(sessionID)
	conversation := d.store.GetSessionConversation(sessionID).NativeID
	linked := d.store.SessionLink(sessionID) == codexLinkKind
	if linked {
		if session := d.store.Get(sessionID); session != nil {
			terminal = codexServerTerminal(session.ProfileID)
		}
	}
	if terminal == "" {
		return nil
	}
	return d.monitors().start(terminal, conversation, sessionID, linked)
}

func (r *monitorRegistry) each() map[protocol.SessionID]*monitorEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make(map[protocol.SessionID]*monitorEntry, len(r.sessions))
	for id, e := range r.sessions {
		entries[id] = e
	}
	return entries
}

func (d *Daemon) forgetMonitor(sessionID protocol.SessionID) {
	r := d.monitors()
	r.mu.Lock()
	r.forgetLocked(sessionID)
	r.mu.Unlock()
	r.rearm()
}

func (r *monitorRegistry) forgetLocked(sessionID protocol.SessionID) {
	e := r.sessions[sessionID]
	if e == nil {
		return
	}
	m := e.monitor
	delete(r.sessions, sessionID)
	delete(m.entries, e)
	if len(m.entries) == 0 && r.physical[m.terminalID] == m {
		delete(r.physical, m.terminalID)
	}
	e.mu.Lock()
	if e.silence != nil {
		e.silence.Stop()
		e.silence = nil
	}
	e.resolvePending = false
	e.mu.Unlock()
}

func (r *monitorRegistry) rearm() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
