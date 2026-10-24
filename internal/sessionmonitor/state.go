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

func (e *Entry) ApplyState(change Change) bool {
	if e == nil {
		return false
	}
	if _, ok := EffectsOf(change.Cause); !ok {
		e.logf("state update discarded: session=%s state=%s cause=unknown", e.Label(), change.State)
		e.TraceChange(change, statetrace.OutcomeDiscarded, "unknown_cause")
		return false
	}
	e.mu.Lock()
	accepted := e.changeStateLocked(change.State, change.Cause, change.RequestStartedAt, time.Now())
	var view View
	var endedSnooze time.Time
	if accepted {
		e.state.Origin = change.Origin
		e.state.Cause = CauseName(change.Cause)
		if effects, _ := EffectsOf(change.Cause); effects.Touch {
			e.touchedAt = time.Now()
		}
		if e.firstReportAt.IsZero() && HarnessReportedState(change.Origin.Source) {
			e.firstReportAt = time.Now()
		}
		deadline := e.turn.SnoozedUntil
		e.openTurnLocked(time.Now(), e.policy.QuietNotifications && e.input.stopFollowsNotificationLocked())
		if !deadline.IsZero() && e.turn.SnoozedUntil.IsZero() {
			endedSnooze = deadline
		}
		e.syncAutoSettleLocked()
		view = e.viewLocked()
	}
	e.mu.Unlock()
	if !accepted || !e.publish(view) {
		e.logf("state update discarded: session=%s state=%s cause=%s", e.Label(), change.State, CauseName(change.Cause))
		e.TraceChange(change, statetrace.OutcomeDiscarded, "store_rejected")
		return false
	}
	if !endedSnooze.IsZero() && e.registry.host.Debug() {
		cause := "broken"
		if !endedSnooze.After(time.Now()) {
			cause = "expired"
		}
		e.logf("snooze %s: session=%s state=%s reason=%s", cause, e.Label(), change.State, view.State.V.Reason)
	}
	return true
}
