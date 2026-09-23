package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

type sessionStateCause interface {
	isSessionStateCause()
}

type liveSignal struct{}

type resolverObservation struct{}

type pluginReport struct {
	runID string
	seq   uint64
}

type startupRecovery struct{}

type hostExitRecovery struct{}

type pluginDriverSilent struct{}

func (liveSignal) isSessionStateCause()          {}
func (pluginDriverSilent) isSessionStateCause()  {}
func (resolverObservation) isSessionStateCause() {}
func (pluginReport) isSessionStateCause()        {}
func (startupRecovery) isSessionStateCause()     {}
func (hostExitRecovery) isSessionStateCause()    {}

type sessionStateChange struct {
	sessionID        string
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
	syncNudge bool
	broadcast bool
}

func stateEffectInstanceFor(cause sessionStateCause) (stateEffectInstance, bool) {
	switch cause.(type) {
	case liveSignal:
		return stateEffectInstance{touch: true, syncNudge: true, broadcast: true}, true
	case resolverObservation:
		return stateEffectInstance{syncNudge: true, broadcast: true}, true
	case pluginReport:
		return stateEffectInstance{touch: true, syncNudge: true, broadcast: true}, true
	case startupRecovery:
		return stateEffectInstance{}, true
	case hostExitRecovery:
		return stateEffectInstance{syncNudge: true, broadcast: true}, true
	case pluginDriverSilent:
		return stateEffectInstance{syncNudge: true, broadcast: true}, true
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
	case pluginReport:
		return "plugin_report"
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

	d.autoSettleFireMu.Lock()
	var inputLane *sessionInputLane
	if instance.syncNudge {
		inputLane = d.sessionInputs().lane(change.sessionID)
		inputLane.mu.Lock()
	}
	applied := d.commitSessionState(change)
	if inputLane != nil {
		inputLane.mu.Unlock()
	}
	d.autoSettleFireMu.Unlock()
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
	d.updateTranscriptWatcherState(change.sessionID, protocol.SessionState(change.state))
	d.traceStateChange(change, statetrace.OutcomeApplied, "")

	if attention.OpensTurn(protocol.SessionState(change.state)) &&
		!d.snoozeSuppressesTurn(change.sessionID, protocol.SessionState(change.state)) {
		d.store.OpenTurnIfClosed(change.sessionID, time.Now())
		d.enqueueSessionActivity(change.sessionID)
	}

	if instance.touch {
		d.store.Touch(change.sessionID)
	}
	if instance.syncNudge {
		d.syncNudgeForState(change.sessionID, change.state)
	}
	d.syncAutoSettle(change.sessionID, change.state)
	if instance.broadcast {
		d.broadcastSessionStateChanged(change.sessionID)
	}
	d.drainAgentMailboxAfterStateChange(change.sessionID, change.state)
	return true
}

func (d *Daemon) commitSessionState(change sessionStateChange) bool {
	switch cause := change.cause.(type) {
	case liveSignal, startupRecovery, resolverObservation, hostExitRecovery, pluginDriverSilent:
		return d.store.UpdateState(change.sessionID, change.state)
	case pluginReport:
		return d.store.ApplyAgentDriverState(
			change.sessionID,
			cause.runID,
			cause.seq,
			change.state,
			change.requestStartedAt,
		)
	default:
		return false
	}
}
