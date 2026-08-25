package daemon

import (
	"sync"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

// A tripwire, not a deadline: the slowest legitimate path is ~65s, and across a 60s
// daemon outage on a live pi session the replacement driver reported after 8s.
const pluginDriverSilenceGrace = 2 * time.Minute

type pluginDriverSilenceWatch struct {
	mu     sync.Mutex
	armed  map[string]*time.Timer
	closed bool
}

func newPluginDriverSilenceWatch() *pluginDriverSilenceWatch {
	return &pluginDriverSilenceWatch{armed: map[string]*time.Timer{}}
}

func (w *pluginDriverSilenceWatch) arm(sessionID string, grace time.Duration, fire func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if timer, ok := w.armed[sessionID]; ok {
		timer.Stop()
	}
	w.armed[sessionID] = time.AfterFunc(grace, fire)
}

func (w *pluginDriverSilenceWatch) disarm(sessionID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	timer, ok := w.armed[sessionID]
	if !ok {
		return false
	}
	timer.Stop()
	delete(w.armed, sessionID)
	return true
}

// stop refuses new alarms for daemon shutdown: a timer that fires into a stopped
// daemon writes to a closed store.
func (w *pluginDriverSilenceWatch) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	for id, timer := range w.armed {
		timer.Stop()
		delete(w.armed, id)
	}
}

func (d *Daemon) pluginDriverSilence() *pluginDriverSilenceWatch {
	d.pluginDriverSilenceOnce.Do(func() {
		d.pluginDriverSilenceWatch = newPluginDriverSilenceWatch()
	})
	return d.pluginDriverSilenceWatch
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

func (d *Daemon) armPluginDriverSilenceWatchForRun(sessionID, runID, pluginName string) {
	grace := d.pluginDriverSilenceGrace()
	if grace <= 0 {
		return
	}
	d.pluginDriverSilence().arm(sessionID, grace, func() {
		d.declarePluginDriverSilent(sessionID, runID, pluginName, grace)
	})
}

func (d *Daemon) notePluginDriverReport(sessionID string) {
	if d.pluginDriverSilence().disarm(sessionID) {
		d.logf("plugin driver silence cleared: session=%s", sessionID)
	}
}

func (d *Daemon) forgetPluginDriverSilenceWatch(sessionID string) {
	d.pluginDriverSilence().disarm(sessionID)
}

func (d *Daemon) pluginDriverSilenceGrace() time.Duration {
	if d.pluginDriverSilenceGraceOverride > 0 {
		return d.pluginDriverSilenceGraceOverride
	}
	return pluginDriverSilenceGrace
}

func (d *Daemon) declarePluginDriverSilent(sessionID, runID, pluginName string, grace time.Duration) {
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
