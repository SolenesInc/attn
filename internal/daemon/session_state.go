package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/attention"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
	"github.com/victorarias/attn/internal/store"
)

type sessionStateCause interface {
	isSessionStateCause()
}

type liveSignal struct{}

type resolverObservation struct{}

type linkTurn struct {
	run string
	seq uint64
}

type startupRecovery struct{}

type hostExitRecovery struct{}

type pluginDriverSilent struct{}

func (liveSignal) isSessionStateCause()          {}
func (pluginDriverSilent) isSessionStateCause()  {}
func (resolverObservation) isSessionStateCause() {}
func (linkTurn) isSessionStateCause()            {}
func (startupRecovery) isSessionStateCause()     {}
func (hostExitRecovery) isSessionStateCause()    {}

type sessionStateChange struct {
	sessionID        protocol.SessionID
	state            string
	cause            sessionStateCause
	requestStartedAt time.Time
	origin           stateOrigin
}

type stateOrigin struct {
	source     string
	detail     string
	observedAt time.Time
}

type stateEffectInstance struct {
	touch     bool
	broadcast bool
}

func stateEffectInstanceFor(cause sessionStateCause) (stateEffectInstance, bool) {
	switch cause.(type) {
	case liveSignal:
		return stateEffectInstance{touch: true, broadcast: true}, true
	case resolverObservation:
		return stateEffectInstance{broadcast: true}, true
	case linkTurn:
		return stateEffectInstance{touch: true, broadcast: true}, true
	case startupRecovery:
		return stateEffectInstance{}, true
	case hostExitRecovery:
		return stateEffectInstance{broadcast: true}, true
	case pluginDriverSilent:
		return stateEffectInstance{broadcast: true}, true
	default:
		return stateEffectInstance{}, false
	}
}

func sessionStateCauseName(cause sessionStateCause) string {
	switch cause.(type) {
	case liveSignal:
		return "live_signal"
	case resolverObservation:
		return "resolver_observation"
	case linkTurn:
		return "link_turn"
	case startupRecovery:
		return "startup_recovery"
	case hostExitRecovery:
		return "host_exit_recovery"
	case pluginDriverSilent:
		return "plugin_driver_silent"
	default:
		return "unknown"
	}
}

func (d *Daemon) applyState(change sessionStateChange) bool {
	if d.store == nil {
		return false
	}
	instance, ok := stateEffectInstanceFor(change.cause)
	if !ok {
		d.logf("state update discarded: session=%s state=%s cause=unknown", change.sessionID, change.state)
		d.traceStateChange(change, statetrace.OutcomeDiscarded, "unknown_cause")
		return false
	}

	entry := d.monitorEntry(change.sessionID)
	var applied, opens, held bool
	var endedSnooze time.Time
	if entry == nil {
		opening := d.turnOpeningFor(change.sessionID, protocol.SessionState(change.state))
		var outcome store.TurnOpeningOutcome
		applied, outcome = d.commitSessionState(change, opening)
		opens, held, endedSnooze = opening.Opens, outcome.HeldBySnooze, outcome.EndedSnooze
	} else {
		entry.mu.Lock()
		accepted := entry.changeStateLocked(change.state, change.cause, change.requestStartedAt, time.Now())
		var view monitorView
		if accepted {
			entry.state.Origin = change.origin
			entry.state.Cause = sessionStateCauseName(change.cause)
			if entry.firstReportAt.IsZero() && harnessReportedState(change.origin.source) {
				entry.firstReportAt = time.Now()
			}
			deadline := entry.turn.SnoozedUntil
			opens = attention.OpensTurn(entry.state.State)
			entry.openTurnLocked(time.Now(), d.quietNotifications() && entry.input.stopFollowsNotificationLocked())
			held = opens && !entry.turn.SnoozedUntil.IsZero()
			if !deadline.IsZero() && entry.turn.SnoozedUntil.IsZero() {
				endedSnooze = deadline
			}
			d.syncAutoSettleLocked(change.sessionID, entry)
			view = entry.viewLocked()
		}
		entry.mu.Unlock()
		applied = accepted && entry.sink.Applied(view)
	}

	if !applied {
		d.logf(
			"state update discarded: session=%s state=%s cause=%s",
			change.sessionID,
			change.state,
			sessionStateCauseName(change.cause),
		)
		d.traceStateChange(change, statetrace.OutcomeDiscarded, "store_rejected")
		return false
	}
	if entry != nil {
		if !endedSnooze.IsZero() {
			d.dropEndedSnoozeWake(change.sessionID, change.state, endedSnooze)
		}
		return true
	}
	d.updateTranscriptWatcherState(change.sessionID, protocol.SessionState(change.state))
	d.traceStateChange(change, statetrace.OutcomeApplied, "")

	if opens && !held {
		d.dropEndedSnoozeWake(change.sessionID, change.state, endedSnooze)
		d.enqueueSessionActivity(change.sessionID)
	}

	if instance.touch {
		d.store.Touch(change.sessionID)
	}
	d.syncAutoSettle(change.sessionID, change.state)
	d.kickSessionInbox(change.sessionID, change.state)
	if instance.broadcast {
		d.broadcastSessionStateChanged(change.sessionID)
	}

	if _, resolved := change.cause.(resolverObservation); !resolved {
		d.resolveSoon(change.sessionID)
	}
	return true
}

func (d *Daemon) commitSessionState(change sessionStateChange, opening store.TurnOpening) (bool, store.TurnOpeningOutcome) {
	switch cause := change.cause.(type) {
	case liveSignal, startupRecovery, resolverObservation, hostExitRecovery, pluginDriverSilent:
		return d.store.UpdateStateOpeningTurn(change.sessionID, change.state, opening)
	case linkTurn:
		return d.store.ApplyAgentDriverStateOpeningTurn(
			change.sessionID,
			cause.run,
			cause.seq,
			change.state,
			change.requestStartedAt,
			opening,
		)
	default:
		return false, store.TurnOpeningOutcome{}
	}
}
