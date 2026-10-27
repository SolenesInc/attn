package sessionmonitor

import (
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/statetrace"
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
	if driver.Run != "" && driver.TranscriptPath != "" && (driver.Run != previous.Driver.V.Run || driver.TranscriptPath != previous.Driver.V.TranscriptPath) {
		pausepoint.At(pausepoint.DriverTranscriptApplied)
		m.WatchDriverUsage(driver.TranscriptPath)
	}
	state := view.State.V
	if state.Sequence != previous.State.V.Sequence && !state.Quiet {
		m.UpdateTranscriptState(state.State)
		source := state.Origin.Source
		if source == "" {
			source = state.Cause
		}
		m.Trace(statetrace.Observation{Source: source, Claim: string(state.State), Detail: state.Origin.Detail, Cause: state.Cause, Outcome: statetrace.OutcomeApplied, ObservedAt: state.Origin.ObservedAt})
		if state.Resolve {
			m.ResolveSoon()
		}
	}
}
