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

// Commits through UpdateState, not the run cursor: advancing the cursor would
// make the driver's own next report the one that gets discarded.
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

type stateEffectProfile struct {
	touch     bool
	syncNudge bool
	broadcast bool
}

func stateEffectProfileFor(cause sessionStateCause) (stateEffectProfile, bool) {
	switch cause.(type) {
	case liveSignal:
		return stateEffectProfile{touch: true, syncNudge: true, broadcast: true}, true
	case resolverObservation:
		return stateEffectProfile{syncNudge: true, broadcast: true}, true
	case pluginReport:
		return stateEffectProfile{touch: true, syncNudge: true, broadcast: true}, true
	case startupRecovery:
		return stateEffectProfile{}, true
	case hostExitRecovery:
		return stateEffectProfile{syncNudge: true, broadcast: true}, true
	case pluginDriverSilent:
		return stateEffectProfile{syncNudge: true, broadcast: true}, true
	default:
		return stateEffectProfile{}, false
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
	profile, ok := stateEffectProfileFor(change.cause)
	if !ok {
		d.logf("state update discarded: session=%s state=%s cause=unknown", change.sessionID, change.state)
		d.traceStateChange(change, statetrace.OutcomeDiscarded, "unknown_cause")
		return false
	}

	// Every state write must be ordered against the auto-settle fire timer.
	d.autoSettleFireMu.Lock()
	var inputLane *sessionInputLane
	if profile.syncNudge {
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
	d.traceStateChange(change, statetrace.OutcomeApplied, "")

	// A snooze suppresses only the turn open: the state is still committed and
	// broadcast.
	if attention.OpensTurn(protocol.SessionState(change.state)) &&
		!d.snoozeSuppressesTurn(change.sessionID, protocol.SessionState(change.state)) {
		d.store.OpenTurnIfClosed(change.sessionID, time.Now())
		// Breaks through the tier's interval but NOT `away`: measured, generating
		// for an empty room would cost nearly half of always-on.
		d.enqueueSessionActivity(change.sessionID)
	}

	if profile.touch {
		d.store.Touch(change.sessionID)
	}
	if profile.syncNudge {
		d.syncNudgeForState(change.sessionID, change.state)
	}
	// After the turn open above, or a state that opens a turn and is `working`
	// is seen half-applied.
	d.syncAutoSettle(change.sessionID, change.state)
	if profile.broadcast {
		d.broadcastSessionStateChanged(change.sessionID)
	}
	d.drainAgentMessagesAfterStateChange(change.sessionID, change.state)
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
