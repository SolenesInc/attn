package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const pluginDriverSilenceGrace = 2 * time.Minute

func (m *Monitor) ArmDriverSilence(run, plugin string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registry.closed.Load() {
		return
	}
	if m.silence != nil {
		m.silence.Stop()
	}
	m.silence = m.registry.host.AfterFunc("pluginDriverSilence", pluginDriverSilenceGrace, func() { m.declareDriverSilent(run, plugin) })
}

func (m *Monitor) declareDriverSilent(run, plugin string) {
	m.mu.Lock()
	state, current := m.state.State, m.driver.Run
	label := m.label
	m.mu.Unlock()
	if current != run || !pluginDriverDeclaredStates[state] {
		return
	}
	m.logf("plugin driver silent: session=%s plugin=%s run=%s state=%s no report for %s; declaring unknown",
		label, plugin, run, state, pluginDriverSilenceGrace)
	m.ApplyState(Change{
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

func (m *Monitor) NoteDriverReport() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.silence == nil {
		return false
	}
	m.silence.Stop()
	m.silence = nil
	return true
}
