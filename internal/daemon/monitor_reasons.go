package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func (d *Daemon) stateReason(sessionID protocol.SessionID) string {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reason
}

func (d *Daemon) clearStateReason(sessionID protocol.SessionID) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		e.reason = ""
		e.mu.Unlock()
	}
}

func (d *Daemon) recordStateReason(sessionID protocol.SessionID, resolution sessionstate.Resolution) bool {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	reason := string(resolution.Reason)
	changed := e.reason != reason
	e.reason = reason
	return changed
}

func (d *Daemon) decorateSessionWithStateReason(clone *protocol.Session) {
	if clone == nil {
		return
	}
	clone.StateReason = nil
	if !resolverOwnedStates[clone.State] {
		return
	}
	if reason := d.stateReason(clone.ID); reason != "" {
		clone.StateReason = protocol.Ptr(reason)
	}
}
