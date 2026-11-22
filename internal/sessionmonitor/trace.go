package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

const (
	StateSourceHook            = "hook_state"
	StateSourceStopHook        = "hook_stop"
	StateSourceClassifier      = "classifier"
	StateSourcePluginDriver    = "plugin_driver"
	StateSourceLink            = "link"
	StateSourceServerReport    = "server_report"
	StateSourceServerQuiet     = "server_quiet"
	StateSourceHookNotify      = "hook_notify"
	StateSourceHookStopFailure = "hook_stop_failure"
	StateSourceHookCompaction  = "hook_compaction"
	StateSourcePermissionMode  = "permission_mode"
	StateSourceResolver        = "resolver"
)

// Reported records the harness's first report of this launch.
func (m *Monitor) Reported() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if !m.firstReportAt.IsZero() {
		m.mu.Unlock()
		return
	}
	m.firstReportAt = time.Now()
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func Stamped(obs statetrace.Observation) statetrace.Observation {
	if obs.RecordedAt.IsZero() {
		obs.RecordedAt = time.Now()
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = obs.RecordedAt
	}
	return obs
}

func (m *Monitor) Trace(obs statetrace.Observation) {
	if m == nil {
		return
	}
	obs = Stamped(obs)
	m.mu.Lock()
	m.recordLocked(obs)
	m.mu.Unlock()
	m.logf("%s", obs.LogLine(protocol.SessionID(m.Label())))
}

func (m *Monitor) traceApplied(view View) {
	state := view.State.V
	m.mu.Lock()
	if state.Sequence <= m.appliedState {
		m.mu.Unlock()
		return
	}
	m.appliedState = state.Sequence
	if state.Quiet {
		m.mu.Unlock()
		return
	}
	source := state.Origin.Source
	if source == "" {
		source = state.Cause
	}
	obs := Stamped(statetrace.Observation{Source: source, Claim: string(state.State), Detail: state.Origin.Detail, Cause: state.Cause, Outcome: statetrace.OutcomeApplied, ObservedAt: state.Origin.ObservedAt})
	m.recordLocked(obs)
	m.mu.Unlock()
	m.logf("%s", obs.LogLine(protocol.SessionID(m.Label())))
}

func (m *Monitor) recordLocked(obs statetrace.Observation) {
	if m.trace == nil {
		m.trace = statetrace.NewRing(statetrace.DefaultCapacity)
	}
	m.trace.Record(obs)
}

func (m *Monitor) TraceSnapshot() []statetrace.Observation {
	if m == nil {
		return nil
	}
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.trace.Snapshot()
}

func ChangeObservation(change Change, outcome statetrace.Outcome, reason string) statetrace.Observation {
	source := change.Origin.Source
	if source == "" {
		source = CauseName(change.Cause)
	}
	return statetrace.Observation{
		Source:     source,
		Claim:      change.State,
		Detail:     change.Origin.Detail,
		Cause:      CauseName(change.Cause),
		Outcome:    outcome,
		Reason:     reason,
		ObservedAt: change.Origin.ObservedAt,
	}
}

func (m *Monitor) TraceChange(change Change, outcome statetrace.Outcome, reason string) {
	if m == nil {
		return
	}
	m.Trace(ChangeObservation(change, outcome, reason))
}
