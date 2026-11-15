package sessionmonitor

type serverCompletions struct {
	observed int64
	applied  int64
}

func (c *serverCompletions) applyLocked(completion int64) bool {
	if completion <= c.applied {
		return false
	}
	c.applied = completion
	return true
}

func (c *serverCompletions) restoreLocked(completion int64) {
	c.observed = max(c.observed, completion)
	c.applied = max(c.applied, completion)
}

func (m *Monitor) RecordServerCompletion(completion int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if completion > m.completions.observed {
		m.completions.observed = completion
		m.resolveSoonLocked()
	}
}

func (m *Monitor) ObservedServerCompletion() int64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.completions.observed
}

func (m *Monitor) endsUnseenServerRun(completion int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return completion > m.completions.applied
}
