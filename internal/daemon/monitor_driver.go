package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) driverInputFromRecord(session protocol.SessionID) monitorDriverMetadata {
	cursor := d.store.GetAgentDriverRun(session)
	return monitorDriverMetadata{Plugin: cursor.PluginName, Run: cursor.RunID, Sequence: cursor.Seq,
		Metadata: d.store.GetAgentMetadata(session), TranscriptPath: d.store.GetAgentDriverTranscriptPath(session)}
}

func (d *Daemon) beginAgentDriverRun(session protocol.SessionID, plugin, run string) bool {
	entry := d.monitorEntry(session)
	if entry == nil {
		return d.store.BeginAgentDriverRun(session, plugin, run)
	}
	entry.mu.Lock()
	if !d.store.BeginAgentDriverRun(session, plugin, run) {
		entry.mu.Unlock()
		return false
	}
	entry.driver = d.driverInputFromRecord(session)
	view := entry.viewLocked()
	entry.mu.Unlock()
	entry.sink.Applied(view)
	return true
}

func (d *Daemon) endAgentDriverRun(session protocol.SessionID) store.AgentDriverReportCursor {
	entry := d.monitorEntry(session)
	if entry == nil {
		return d.store.EndAgentDriverRun(session)
	}
	entry.mu.Lock()
	run := d.store.EndAgentDriverRun(session)
	if run.RunID == "" {
		entry.mu.Unlock()
		return run
	}
	entry.driver = monitorDriverMetadata{Metadata: entry.driver.Metadata}
	view := entry.viewLocked()
	entry.mu.Unlock()
	entry.sink.Applied(view)
	return run
}

func (d *Daemon) retireMonitorDriver(session protocol.SessionID, run string) {
	entry := d.monitorEntry(session)
	if entry == nil {
		return
	}
	entry.mu.Lock()
	if run == "" || entry.driver.Run != run {
		entry.mu.Unlock()
		return
	}
	entry.driver = monitorDriverMetadata{Metadata: entry.driver.Metadata}
	view := entry.viewLocked()
	entry.mu.Unlock()
	entry.sink.Applied(view)
}

func (e *monitorEntry) reportDriverMetadata(run string, sequence uint64, metadata string) bool {
	e.mu.Lock()
	if run == "" || e.driver.Run != run || sequence <= e.driver.Sequence {
		e.mu.Unlock()
		return false
	}
	e.driver.Sequence = sequence
	e.driver.Metadata = strings.TrimSpace(metadata)
	view := e.viewLocked()
	e.mu.Unlock()
	return e.sink.Applied(view)
}

func (e *monitorEntry) reportDriverTranscript(run, path string) bool {
	e.mu.Lock()
	if run == "" || e.driver.Run != run {
		e.mu.Unlock()
		return false
	}
	e.driver.TranscriptPath = path
	view := e.viewLocked()
	e.mu.Unlock()
	pausepoint.At(pausepoint.DriverTranscriptCaptured)
	return e.sink.Applied(view)
}
