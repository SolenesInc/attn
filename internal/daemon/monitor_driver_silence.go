package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const pluginDriverSilenceGrace = 2 * time.Minute

func (d *Daemon) stopPluginDriverSilence() {
	r := d.monitors()
	r.closed.Store(true)
	for id := range r.each() {
		d.forgetPluginDriverSilenceWatch(id)
	}
}

func (d *Daemon) armPluginDriverSilenceWatch(pluginName string) {
	if d.store == nil {
		return
	}
	for _, run := range d.store.ListAgentDriverRuns(pluginName) {
		d.armPluginDriverSilenceWatchForRun(run.SessionID, run.RunID, pluginName)
	}
}

func (d *Daemon) armPluginDriverSilenceWatchForEveryRun() {
	if d.store == nil {
		return
	}
	for _, run := range d.store.ListActiveAgentDriverRuns() {
		d.armPluginDriverSilenceWatchForRun(run.SessionID, run.RunID, run.PluginName)
	}
}

func (d *Daemon) armPluginDriverSilenceWatchForRun(sessionID protocol.SessionID, runID string, pluginName string) {
	grace := pluginDriverSilenceGrace
	r := d.monitors()
	e := r.entry(sessionID)
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.closed.Load() {
		return
	}

	if e.silence != nil {
		e.silence.Stop()
	}
	e.silence = d.life.AfterFunc("pluginDriverSilence", grace, func() { d.declarePluginDriverSilent(sessionID, runID, pluginName, grace) })
}

func (d *Daemon) notePluginDriverReport(sessionID protocol.SessionID) {
	if d.disarmPluginDriverSilence(sessionID) {
		d.logf("plugin driver silence cleared: session=%s", sessionID)
	}
}

func (d *Daemon) forgetPluginDriverSilenceWatch(sessionID protocol.SessionID) {
	d.disarmPluginDriverSilence(sessionID)
}

func (d *Daemon) declarePluginDriverSilent(sessionID protocol.SessionID, runID string, pluginName string, grace time.Duration) {
	if d.store == nil {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	if run := d.store.GetAgentDriverRun(sessionID); run.RunID != runID {
		return
	}
	if !pluginDriverDeclaredStates[session.State] {
		return
	}
	d.logf(
		"plugin driver silent: session=%s plugin=%s run=%s state=%s no report for %s; declaring unknown",
		sessionID, pluginName, runID, session.State, grace,
	)
	d.applyState(sessionStateChange{
		sessionID: sessionID,
		state:     protocol.StateUnknown,
		cause:     pluginDriverSilent{},
		origin:    stateOrigin{source: stateSourcePluginDriver, detail: "driver silent"},
	})
}

var pluginDriverDeclaredStates = map[protocol.SessionState]bool{
	protocol.SessionStateWorking:         true,
	protocol.SessionStateIdle:            true,
	protocol.SessionStateWaitingInput:    true,
	protocol.SessionStatePendingApproval: true,
}

func (d *Daemon) disarmPluginDriverSilence(sessionID protocol.SessionID) bool {
	e := d.monitorEntry(sessionID)
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.silence == nil {
		return false
	}
	e.silence.Stop()
	e.silence = nil
	return true
}
