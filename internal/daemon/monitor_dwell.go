package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type dwellPending struct {
	state protocol.SessionState
	until time.Time
}

func (d *Daemon) dwellReady(sessionID protocol.SessionID, state protocol.SessionState, dwell time.Duration, now time.Time) bool {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if dwell <= 0 {
		e.dwell = dwellPending{}
		return true
	}
	if e.dwell.state != state || e.dwell.until.IsZero() {
		e.dwell = dwellPending{state: state, until: now.Add(dwell)}
		return false
	}
	if now.Before(e.dwell.until) {
		return false
	}
	e.dwell = dwellPending{}
	return true
}

func (d *Daemon) dwellDeadline(sessionID protocol.SessionID) time.Time {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return time.Time{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dwell.until
}

func (d *Daemon) clearDwell(sessionID protocol.SessionID) {
	if e := d.monitorEntry(sessionID); e != nil {
		e.mu.Lock()
		e.dwell = dwellPending{}
		e.mu.Unlock()
	}
}
