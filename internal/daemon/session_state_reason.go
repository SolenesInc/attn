package daemon

import (
	"sync"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

type sessionStateReasons struct {
	mu      sync.Mutex
	reasons map[protocol.SessionID]string
}

func newSessionStateReasons() *sessionStateReasons {
	return &sessionStateReasons{reasons: make(map[protocol.SessionID]string)}
}

func (r *sessionStateReasons) set(sessionID protocol.SessionID, reason string) bool {
	if r == nil || protocol.TrimID(sessionID) == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reasons[sessionID] == reason {
		return false
	}
	r.reasons[sessionID] = reason
	return true
}

func (r *sessionStateReasons) get(sessionID protocol.SessionID) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reasons[sessionID]
}

func (r *sessionStateReasons) forget(sessionID protocol.SessionID) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.reasons, sessionID)
}

func (d *Daemon) stateReasons() *sessionStateReasons {
	d.sessionStateReasonOnce.Do(func() {
		d.sessionStateReason = newSessionStateReasons()
	})
	return d.sessionStateReason
}

func (d *Daemon) recordStateReason(sessionID protocol.SessionID, resolution sessionstate.Resolution) bool {
	return d.stateReasons().set(sessionID, string(resolution.Reason))
}

func (d *Daemon) decorateSessionWithStateReason(clone *protocol.Session) {
	if clone == nil {
		return
	}
	clone.StateReason = nil
	if !resolverOwnedStates[clone.State] {
		return
	}
	if reason := d.stateReasons().get(clone.ID); reason != "" {
		clone.StateReason = protocol.Ptr(reason)
	}
}
