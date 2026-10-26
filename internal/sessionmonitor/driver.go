package sessionmonitor

import (
	"strings"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/ptybackend"
)

func (m *Monitor) BeginDriverRun(plugin, run string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	previous := m.driver
	m.driver = DriverMetadata{Plugin: plugin, Run: run, Metadata: previous.Metadata}
	view := m.viewLocked()
	m.mu.Unlock()
	if m.publish(view) {
		return true
	}
	m.mu.Lock()
	if m.driver.Run == run {
		m.driver = previous
	}
	view = m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
	return false
}

func (m *Monitor) RestoreDriverRun(run DriverMetadata) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.driver.Run != "" {
		m.mu.Unlock()
		return
	}
	run.Metadata = m.driver.Metadata
	run.TranscriptPath = m.driver.TranscriptPath
	m.driver = run
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func (m *Monitor) EndDriverRun() DriverMetadata {
	if m == nil {
		return DriverMetadata{}
	}
	m.mu.Lock()
	ended := m.driver
	if ended.Run == "" {
		m.mu.Unlock()
		return DriverMetadata{}
	}
	m.driver = DriverMetadata{Metadata: ended.Metadata, TranscriptPath: ended.TranscriptPath}
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
	return ended
}

func (m *Monitor) Driver() DriverMetadata {
	if m == nil {
		return DriverMetadata{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.driver
}

func (m *Monitor) RetireDriver(run string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if run == "" || m.driver.Run != run {
		m.mu.Unlock()
		return
	}
	m.driver = DriverMetadata{Metadata: m.driver.Metadata, TranscriptPath: m.driver.TranscriptPath}
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func (m *Monitor) ReportDriverMetadata(run string, sequence uint64, metadata string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if run == "" || m.driver.Run != run || sequence <= m.driver.Sequence {
		m.mu.Unlock()
		return false
	}
	m.driver.Sequence = sequence
	m.driver.Metadata = strings.TrimSpace(metadata)
	view := m.viewLocked()
	m.mu.Unlock()
	return m.publish(view)
}

func (m *Monitor) ReportDriverTranscript(run, path string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if run == "" || m.driver.Run != run {
		m.mu.Unlock()
		return false
	}
	m.driver.TranscriptPath = path
	view := m.viewLocked()
	m.mu.Unlock()
	pausepoint.At(pausepoint.DriverTranscriptCaptured)
	return m.publish(view)
}

func (m *Monitor) BeginDriverLaunch(plugin, run string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.driverLaunch = &driverLaunch{plugin: plugin, run: run}
}

func (m *Monitor) QueueDuringDriverLaunch(plugin, run string, replay func(), whileQueued func()) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.driverLaunch
	if launch == nil || launch.plugin != plugin || launch.run != run {
		return false
	}
	launch.replays = append(launch.replays, replay)
	whileQueued()
	return true
}

func (m *Monitor) QueueExitDuringDriverLaunch(info ptybackend.ExitInfo, whileQueued func()) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.driverLaunch
	if launch == nil || info.LifecycleID == "" || launch.run != info.LifecycleID {
		return false
	}
	launch.exit = &info
	whileQueued()
	return true
}

func (m *Monitor) SupersededExitDuringDriverLaunch(info ptybackend.ExitInfo) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.driverLaunch
	return launch != nil && info.LifecycleID != "" && launch.run != info.LifecycleID
}

func (m *Monitor) FinishDriverLaunch() (replays []func(), exit *ptybackend.ExitInfo) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.driverLaunch
	m.driverLaunch = nil
	if launch == nil {
		return nil, nil
	}
	return launch.replays, launch.exit
}

func (m *Monitor) AbortDriverLaunch() (plugin, run string, launching bool) {
	if m == nil {
		return "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	launch := m.driverLaunch
	m.driverLaunch = nil
	if launch == nil {
		return "", "", false
	}
	return launch.plugin, launch.run, true
}
