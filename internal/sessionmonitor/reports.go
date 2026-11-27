package sessionmonitor

import (
	"maps"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

func (m *Monitor) ReportHarnessTitle(title string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if m.harnessTitle == title {
		m.mu.Unlock()
		return true
	}
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

func (m *Monitor) ReportSentFiles(paths []string, at time.Time, away bool) bool {
	return m.reportFiles(&m.sentFiles, paths, at, away)
}

func (m *Monitor) ReportEditedFiles(paths []string, at time.Time, away bool) bool {
	return m.reportFiles(&m.editedFiles, paths, at, away)
}

func (m *Monitor) reportFiles(files *map[string]FileReport, paths []string, at time.Time, away bool) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	*files = maps.Clone(*files)
	if *files == nil {
		*files = make(map[string]FileReport)
	}
	for _, path := range paths {
		(*files)[path] = FileReport{Sequence: m.revision + 1, At: at, AwayFromHome: away}
	}
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publishAcknowledged(view)
}

func (m *Monitor) RecordBackgroundLaunch(task string, at time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.launches == nil {
		m.launches = make(map[string]time.Time)
	}
	m.launches[task] = at
}

func (m *Monitor) RecordBackgroundReported() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.backgroundReported {
		m.mu.Unlock()
		return
	}
	m.backgroundReported = true
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func (m *Monitor) RecordBackgroundTasks(tasks []protocol.BackgroundTask, at time.Time) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if len(tasks) == 0 && len(m.backgroundTasks) == 0 {
		m.launches = nil
		m.mu.Unlock()
		return false
	}
	listed := make(map[string]time.Time, len(m.backgroundTasks))
	for _, task := range m.backgroundTasks {
		if id := protocol.Deref(task.ID); id != "" {
			listed[id] = task.StartedAt
		}
	}
	running := make([]BackgroundTask, 0, len(tasks))
	for _, task := range tasks {
		id := protocol.Deref(task.ID)
		started, known := m.launches[id]
		if !known {
			started, known = listed[id]
		}
		if id == "" || !known {
			started = at
		}
		running = append(running, BackgroundTask{BackgroundTask: task, StartedAt: started})
	}
	m.launches = nil
	m.backgroundTasks = running
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
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
	cleared = m.exit != nil && m.exit.At.Before(before)
	if cleared {
		m.exit = nil
	}
	m.watch.mu.Lock()
	processCleared := m.exit == nil &&
		m.watch.terminal.Process != nil &&
		m.watch.terminal.Process.Claim == sessionstate.ClaimExited &&
		(cleared || !m.watch.terminal.Process.ObservedAt.After(before))
	if processCleared {
		m.watch.terminal.Process = nil
	}
	m.watch.mu.Unlock()
	if !cleared && !processCleared {
		m.mu.Unlock()
		return false, false
	}
	if processCleared {
		m.resolveSoonLocked()
	}
	view := m.viewLocked()
	m.mu.Unlock()
	return cleared, m.publish(view)
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
