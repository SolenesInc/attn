package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const pluginDriverSilenceGrace = 2 * time.Minute

func (e *Entry) ArmDriverSilence(run, plugin string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.registry.closed.Load() {
		return
	}
	if e.silence != nil {
		e.silence.Stop()
	}
	e.silence = e.registry.host.AfterFunc("pluginDriverSilence", pluginDriverSilenceGrace, func() { e.declareDriverSilent(run, plugin) })
}

func (e *Entry) declareDriverSilent(run, plugin string) {
	e.mu.Lock()
	state, current := e.state.State, e.driver.Run
	label := e.label
	e.mu.Unlock()
	if current != run || !pluginDriverDeclaredStates[state] {
		return
	}
	e.logf("plugin driver silent: session=%s plugin=%s run=%s state=%s no report for %s; declaring unknown",
		label, plugin, run, state, pluginDriverSilenceGrace)
	e.ApplyState(Change{
		State:  protocol.StateUnknown,
		Cause:  pluginDriverSilent{},
		Origin: Origin{Source: StateSourcePluginDriver, Detail: "driver silent"},
	})
}

var pluginDriverDeclaredStates = map[protocol.SessionState]bool{
	protocol.SessionStateWorking:         true,
	protocol.SessionStateIdle:            true,
	protocol.SessionStateWaitingInput:    true,
	protocol.SessionStatePendingApproval: true,
}

func (e *Entry) NoteDriverReport() bool {
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
