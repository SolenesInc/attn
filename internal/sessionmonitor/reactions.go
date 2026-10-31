package sessionmonitor

import (
	"github.com/victorarias/attn/internal/pausepoint"
)

func (m *Monitor) react(view View) {
	m.mu.Lock()
	previous := m.reacted
	if previous.Revision.V >= view.Revision.V {
		m.mu.Unlock()
		return
	}
	m.reacted = view
	m.mu.Unlock()
	driver := view.Driver.V
	if m.Session() != "" && driver.Run != "" && driver.TranscriptPath != "" && (driver.Run != previous.Driver.V.Run || driver.TranscriptPath != previous.Driver.V.TranscriptPath) {
		pausepoint.At(pausepoint.DriverTranscriptApplied)
		m.WatchDriverUsage(driver.TranscriptPath)
	}
	state := view.State.V
	if state.Sequence != previous.State.V.Sequence && !state.Quiet {
		m.UpdateTranscriptState(state.State)
		if state.Resolve {
			m.ResolveSoon()
		}
	}
}
