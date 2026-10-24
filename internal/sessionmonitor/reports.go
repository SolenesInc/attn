package sessionmonitor

import (
	"maps"
	"time"
)

func (m *Monitor) ReportHarnessTitle(title string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	m.harnessTitle = title
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

func (m *Monitor) ReportPullRequest(id string, link PullRequest) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	m.pullRequests = maps.Clone(m.pullRequests)
	if m.pullRequests == nil {
		m.pullRequests = make(map[string]PullRequest)
	}
	link.Sequence = m.revision + 1
	m.pullRequests[id] = link
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

func (m *Monitor) ReportSentFiles(paths []string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	m.sentFiles = maps.Clone(m.sentFiles)
	if m.sentFiles == nil {
		m.sentFiles = make(map[string]uint64)
	}
	for _, path := range paths {
		m.sentFiles[path] = m.revision + 1
	}
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

func (m *Monitor) ReofferExit(lifecycle string) (offered, applied bool) {
	if m == nil {
		return false, false
	}
	m.mu.Lock()
	if m.exit == nil || lifecycle == "" || m.exit.LifecycleID != lifecycle {
		m.mu.Unlock()
		return false, false
	}
	view := m.viewLocked()
	m.mu.Unlock()
	return true, m.publish(view)
}

func (m *Monitor) ReportExit(exit Exit) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	m.exit = &exit
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

func (m *Monitor) ClearExitBefore(before time.Time) (cleared, applied bool) {
	if m == nil {
		return false, false
	}
	m.mu.Lock()
	if m.exit == nil || !m.exit.At.Before(before) {
		m.mu.Unlock()
		return false, false
	}
	m.exit = nil
	view := m.viewLocked()
	m.mu.Unlock()
	return true, m.publish(view)
}

func (m *Monitor) ForgetPullRequest(id string) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	if _, ok := m.pullRequests[id]; !ok {
		m.mu.Unlock()
		return true
	}
	m.pullRequests = maps.Clone(m.pullRequests)
	delete(m.pullRequests, id)
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}
