package sessionmonitor

import (
	"time"

	"github.com/victorarias/attn/internal/statetrace"
)

type Cause interface {
	isSessionStateCause()
}

type LiveSignal struct{}

type resolverObservation struct{}

type LinkTurn struct {
	Run string
	Seq uint64
}

type StartupRecovery struct{}

type HostExitRecovery struct{}

type pluginDriverSilent struct{}

func (LiveSignal) isSessionStateCause()          {}
func (pluginDriverSilent) isSessionStateCause()  {}
func (resolverObservation) isSessionStateCause() {}
func (LinkTurn) isSessionStateCause()            {}
func (StartupRecovery) isSessionStateCause()     {}
func (HostExitRecovery) isSessionStateCause()    {}

type Change struct {
	State            string
	Cause            Cause
	RequestStartedAt time.Time
	Origin           Origin
}

type Origin struct {
	Source     string
	Detail     string
	ObservedAt time.Time
}

type Effects struct {
	Touch     bool
	Broadcast bool
}

func EffectsOf(cause Cause) (Effects, bool) {
	switch cause.(type) {
	case LiveSignal:
		return Effects{Touch: true, Broadcast: true}, true
	case resolverObservation:
		return Effects{Broadcast: true}, true
	case LinkTurn:
		return Effects{Touch: true, Broadcast: true}, true
	case StartupRecovery:
		return Effects{}, true
	case HostExitRecovery:
		return Effects{Broadcast: true}, true
	case pluginDriverSilent:
		return Effects{Broadcast: true}, true
	default:
		return Effects{}, false
	}
}

func CauseName(cause Cause) string {
	switch cause.(type) {
	case LiveSignal:
		return "live_signal"
	case resolverObservation:
		return "resolver_observation"
	case LinkTurn:
		return "link_turn"
	case StartupRecovery:
		return "startup_recovery"
	case HostExitRecovery:
		return "host_exit_recovery"
	case pluginDriverSilent:
		return "plugin_driver_silent"
	default:
		return "unknown"
	}
}

func Resolved(cause Cause) bool {
	_, resolved := cause.(resolverObservation)
	return resolved
}

func (m *Monitor) ApplyState(change Change) bool {
	if m == nil {
		return false
	}
	if _, ok := EffectsOf(change.Cause); !ok {
		m.logf("state update discarded: session=%s state=%s cause=unknown", m.Label(), change.State)
		m.TraceChange(change, statetrace.OutcomeDiscarded, "unknown_cause")
		return false
	}
	m.mu.Lock()
	accepted := m.changeStateLocked(change.State, change.Cause, change.RequestStartedAt, time.Now())
	var view View
	var endedSnooze time.Time
	if accepted {
		m.state.Origin = change.Origin
		m.state.Cause = CauseName(change.Cause)
		if effects, _ := EffectsOf(change.Cause); effects.Touch {
			m.touchedAt = time.Now()
		}
		if m.firstReportAt.IsZero() && HarnessReportedState(change.Origin.Source) {
			m.firstReportAt = time.Now()
		}
		deadline := m.turn.SnoozedUntil
		m.openTurnLocked(time.Now(), m.policy.QuietNotifications && m.input.stopFollowsNotificationLocked())
		if !deadline.IsZero() && m.turn.SnoozedUntil.IsZero() {
			endedSnooze = deadline
		}
		m.syncAutoSettleLocked()
		view = m.viewLocked()
	}
	m.mu.Unlock()
	if !accepted || !m.publish(view) {
		m.logf("state update discarded: session=%s state=%s cause=%s", m.Label(), change.State, CauseName(change.Cause))
		m.TraceChange(change, statetrace.OutcomeDiscarded, "store_rejected")
		return false
	}
	if !endedSnooze.IsZero() && m.registry.host.Debug() {
		cause := "broken"
		if !endedSnooze.After(time.Now()) {
			cause = "expired"
		}
		m.logf("snooze %s: session=%s state=%s reason=%s", cause, m.Label(), change.State, view.State.V.Reason)
	}
	return true
}
