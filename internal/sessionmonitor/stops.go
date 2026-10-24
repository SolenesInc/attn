package sessionmonitor

import "time"

func (m *Monitor) classifiedTurnID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.classifiedTurn
}

func (m *Monitor) setClassifiedTurnID(turnID string) {
	m.mu.Lock()
	m.classifiedTurn = turnID
	m.mu.Unlock()
}

func (m *Monitor) beginClassifyingTurn(turnID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.classifiedTurn == turnID || m.classifyingTurn == turnID {
		return false
	}
	m.classifyingTurn = turnID
	return true
}

func (m *Monitor) clearClassifyingTurn() {
	m.mu.Lock()
	m.classifyingTurn = ""
	m.mu.Unlock()
}

const forcedStopSuppressTTL = 30 * time.Second

func (m *Monitor) MarkForcedStop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.forcedStop = time.Now()
	m.mu.Unlock()
}

func (m *Monitor) ConsumeForcedStop() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	at := m.forcedStop
	m.forcedStop = time.Time{}
	return !at.IsZero() && time.Since(at) <= forcedStopSuppressTTL
}

func (m *Monitor) ClearForcedStop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.forcedStop = time.Time{}
	m.mu.Unlock()
}
