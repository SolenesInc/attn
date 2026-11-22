package sessionmonitor

import (
	"strings"

	"github.com/victorarias/attn/internal/ptybackend"
)

func (m *Monitor) BeginRun(plugin, run, route string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	previous := m.runRecord
	m.runRecord = RunRecord{Owner: plugin, ID: run, Route: route, Note: previous.Note}
	view := m.viewLocked()
	m.mu.Unlock()
	if m.publish(view) {
		return true
	}
	m.mu.Lock()
	if m.runRecord.ID == run {
		m.runRecord = previous
	}
	view = m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
	return false
}

func (m *Monitor) ResolveRoute(plugin, run, route string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if m.runRecord.Owner != plugin || m.runRecord.ID != run || m.runRecord.Route != "" {
		m.mu.Unlock()
		return false
	}
	m.runRecord.Route = route
	view := m.viewLocked()
	m.mu.Unlock()
	if m.publish(view) {
		return true
	}
	m.mu.Lock()
	if m.runRecord.Owner == plugin && m.runRecord.ID == run && m.runRecord.Route == route {
		m.runRecord.Route = ""
	}
	m.mu.Unlock()
	return false
}

func (m *Monitor) RestoreRun(run RunRecord) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.runRecord.ID != "" {
		m.mu.Unlock()
		return
	}
	run.Note = m.runRecord.Note
	run.Path = m.runRecord.Path
	m.runRecord = run
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func (m *Monitor) EndRun() RunRecord {
	if m == nil {
		return RunRecord{}
	}
	m.mu.Lock()
	ended := m.runRecord
	if ended.ID == "" {
		m.mu.Unlock()
		return RunRecord{}
	}
	m.runRecord = RunRecord{Note: ended.Note, Path: ended.Path}
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
	return ended
}

func (m *Monitor) RunRecord() RunRecord {
	if m == nil {
		return RunRecord{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runRecord
}

func (m *Monitor) RetireRun(run string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if run == "" || m.runRecord.ID != run {
		m.mu.Unlock()
		return false
	}
	m.runRecord = RunRecord{Note: m.runRecord.Note, Path: m.runRecord.Path}
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
	return true
}

func (m *Monitor) ReportRunNote(run string, sequence uint64, metadata string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if run == "" || m.runRecord.ID != run || sequence <= m.runRecord.Sequence {
		m.mu.Unlock()
		return false
	}
	m.runRecord.Sequence = sequence
	m.runRecord.Note = strings.TrimSpace(metadata)
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

func (m *Monitor) ReportRunPath(run, path string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if run == "" || m.runRecord.ID != run {
		m.mu.Unlock()
		return false
	}
	m.runRecord.Path = path
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

func (m *Monitor) BeginLaunch(plugin, run string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.launchGate = &launchGate{owner: plugin, run: run}
}

func (m *Monitor) QueueDuringLaunch(plugin, run string, replay func(), whileQueued func()) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.launchGate
	if launch == nil || launch.owner != plugin || launch.run != run {
		return false
	}
	launch.replays = append(launch.replays, replay)
	whileQueued()
	return true
}

func (m *Monitor) QueueExitDuringLaunch(info ptybackend.ExitInfo, whileQueued func()) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.launchGate
	if launch == nil || info.LifecycleID == "" || launch.run != info.LifecycleID {
		return false
	}
	launch.exit = &info
	whileQueued()
	return true
}

func (m *Monitor) SupersededExitDuringLaunch(info ptybackend.ExitInfo) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.launchGate
	return launch != nil && info.LifecycleID != "" && launch.run != info.LifecycleID
}

func (m *Monitor) FinishLaunch(run string) *ptybackend.ExitInfo {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	launch := m.launchGate
	if launch == nil || launch.run != run {
		m.mu.Unlock()
		return nil
	}
	for launch != nil && m.launchGate == launch {
		replays := launch.replays
		launch.replays = nil
		if len(replays) == 0 {
			m.launchGate = nil
			exit := launch.exit
			m.mu.Unlock()
			return exit
		}
		m.mu.Unlock()
		for _, replay := range replays {
			replay()
		}
		m.mu.Lock()
	}
	m.mu.Unlock()
	return nil
}

func (m *Monitor) AbortLaunch() (plugin, run string, launching bool) {
	if m == nil {
		return "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.launchGate
	m.launchGate = nil
	if launch == nil {
		return "", "", false
	}
	return launch.owner, launch.run, true
}
