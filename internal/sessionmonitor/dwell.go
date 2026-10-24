package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type dwellPending struct {
	state protocol.SessionState
	until time.Time
}

func (e *Entry) dwellReady(state protocol.SessionState, dwell time.Duration, now time.Time) bool {
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

func (e *Entry) dwellDeadline() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dwell.until
}

func (e *Entry) clearDwell() {
	e.mu.Lock()
	e.dwell = dwellPending{}
	e.mu.Unlock()
}
