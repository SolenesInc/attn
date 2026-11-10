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

func (m *Monitor) ReportTerminalTitle(title string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if m.terminalTitle == title {
		m.mu.Unlock()
		return true
	}
	m.terminalTitle = title
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
	return m.publishAcknowledged(view)
}

func (m *Monitor) ReportSentFiles(paths []string, at time.Time) bool {
	return m.reportFiles(&m.sentFiles, paths, at)
}

func (m *Monitor) ReportEditedFiles(paths []string, at time.Time) bool {
	return m.reportFiles(&m.editedFiles, paths, at)
}

func (m *Monitor) reportFiles(files *map[string]FileReport, paths []string, at time.Time) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	*files = maps.Clone(*files)
	if *files == nil {
		*files = make(map[string]FileReport)
	}
	for _, path := range paths {
		(*files)[path] = FileReport{Sequence: m.revision + 1, At: at}
	}
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publishAcknowledged(view)
}

func (m *Monitor) HeldExit(lifecycle string) (Exit, bool) {
	if m == nil {
		return Exit{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.exit == nil || !m.exit.Held || lifecycle == "" || m.exit.LifecycleID != lifecycle {
		return Exit{}, false
	}
	return *m.exit, true
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
	return m.publishAcknowledged(view)
}
