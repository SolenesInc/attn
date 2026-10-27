package sessionmonitor

import "github.com/victorarias/attn/internal/sessionstate"

func (m *Monitor) recordStateReason(resolution sessionstate.Resolution) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	reason := string(resolution.Reason)
	changed := m.reason != reason
	m.reason = reason
	return changed
}
