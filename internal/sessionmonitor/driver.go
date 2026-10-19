package sessionmonitor

import (
	"strings"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/ptybackend"
)

func (e *Entry) BeginDriverRun(begin func() (DriverMetadata, bool)) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	driver, ok := begin()
	if !ok {
		e.mu.Unlock()
		return false
	}
	e.driver = driver
	view := e.viewLocked()
	e.mu.Unlock()
	e.home.Applied(view)
	return true
}

func (e *Entry) EndDriverRun(end func() bool) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if !end() {
		e.mu.Unlock()
		return
	}
	e.driver = DriverMetadata{Metadata: e.driver.Metadata}
	view := e.viewLocked()
	e.mu.Unlock()
	e.home.Applied(view)
}

func (e *Entry) RetireDriver(run string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if run == "" || e.driver.Run != run {
		e.mu.Unlock()
		return
	}
	e.driver = DriverMetadata{Metadata: e.driver.Metadata}
	view := e.viewLocked()
	e.mu.Unlock()
	e.home.Applied(view)
}

func (e *Entry) ReportDriverMetadata(run string, sequence uint64, metadata string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if run == "" || e.driver.Run != run || sequence <= e.driver.Sequence {
		e.mu.Unlock()
		return false
	}
	e.driver.Sequence = sequence
	e.driver.Metadata = strings.TrimSpace(metadata)
	view := e.viewLocked()
	e.mu.Unlock()
	return e.home.Applied(view)
}

func (e *Entry) ReportDriverTranscript(run, path string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if run == "" || e.driver.Run != run {
		e.mu.Unlock()
		return false
	}
	e.driver.TranscriptPath = path
	view := e.viewLocked()
	e.mu.Unlock()
	pausepoint.At(pausepoint.DriverTranscriptCaptured)
	return e.home.Applied(view)
}

func (e *Entry) BeginDriverLaunch(plugin, run string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.driverLaunch = &driverLaunch{plugin: plugin, run: run}
}

func (e *Entry) QueueDuringDriverLaunch(plugin, run string, replay func(), whileQueued func()) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	launch := e.driverLaunch
	if launch == nil || launch.plugin != plugin || launch.run != run {
		return false
	}
	launch.replays = append(launch.replays, replay)
	whileQueued()
	return true
}

func (e *Entry) QueueExitDuringDriverLaunch(info ptybackend.ExitInfo, whileQueued func()) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	launch := e.driverLaunch
	if launch == nil || info.LifecycleID == "" || launch.run != info.LifecycleID {
		return false
	}
	launch.exit = &info
	whileQueued()
	return true
}

func (e *Entry) SupersededExitDuringDriverLaunch(info ptybackend.ExitInfo) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	launch := e.driverLaunch
	return launch != nil && info.LifecycleID != "" && launch.run != info.LifecycleID
}

func (e *Entry) FinishDriverLaunch() (replays []func(), exit *ptybackend.ExitInfo) {
	if e == nil {
		return nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	launch := e.driverLaunch
	e.driverLaunch = nil
	if launch == nil {
		return nil, nil
	}
	return launch.replays, launch.exit
}

func (e *Entry) AbortDriverLaunch() (plugin, run string, launching bool) {
	if e == nil {
		return "", "", false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	launch := e.driverLaunch
	e.driverLaunch = nil
	if launch == nil {
		return "", "", false
	}
	return launch.plugin, launch.run, true
}
