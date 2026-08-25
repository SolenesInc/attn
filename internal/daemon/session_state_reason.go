package daemon

import (
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

type sessionStateReasons struct {
	mu      sync.Mutex
	reasons map[string]string
}

func newSessionStateReasons() *sessionStateReasons {
	return &sessionStateReasons{reasons: make(map[string]string)}
}

// Reports whether the reason changed: it is recomputed every resolver tick and
// almost never moves, so the delta is what keeps the tick off the wire.
func (r *sessionStateReasons) set(sessionID, reason string) bool {
	if r == nil || strings.TrimSpace(sessionID) == "" {
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

func (r *sessionStateReasons) get(sessionID string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reasons[sessionID]
}

func (r *sessionStateReasons) forget(sessionID string) {
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

func (d *Daemon) recordStateReason(sessionID string, resolution sessionstate.Resolution) bool {
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
