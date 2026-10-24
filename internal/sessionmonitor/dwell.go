package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type dwellPending struct {
	state protocol.SessionState
	until time.Time
}

func (m *Monitor) dwellReady(state protocol.SessionState, dwell time.Duration, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if dwell <= 0 {
		m.dwell = dwellPending{}
		return true
	}
	if m.dwell.state != state || m.dwell.until.IsZero() {
		m.dwell = dwellPending{state: state, until: now.Add(dwell)}
		return false
	}
	if now.Before(m.dwell.until) {
		return false
	}
	m.dwell = dwellPending{}
	return true
}

func (m *Monitor) dwellDeadline() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dwell.until
}

func (m *Monitor) clearDwell() {
	m.mu.Lock()
	m.dwell = dwellPending{}
	m.mu.Unlock()
}
