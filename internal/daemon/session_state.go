package daemon

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/statetrace"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) applyState(sessionID protocol.SessionID, change sessionmonitor.Change) bool {
	if d.store == nil {
		return false
	}
	if entry := d.monitorEntry(sessionID); entry != nil {
		return entry.ApplyState(change)
	}
	effects, ok := sessionmonitor.EffectsOf(change.Cause)
	if !ok {
		d.logf("state update discarded: session=%s state=%s cause=unknown", sessionID, change.State)
		d.recordStateObservation(sessionID, sessionmonitor.ChangeObservation(change, statetrace.OutcomeDiscarded, "unknown_cause"))
		return false
	}
	opening := d.turnOpeningFor(sessionID, protocol.SessionState(change.State))
	applied, outcome := d.commitSessionState(sessionID, change, opening)
	if !applied {
		d.logf("state update discarded: session=%s state=%s cause=%s", sessionID, change.State, sessionmonitor.CauseName(change.Cause))
		d.recordStateObservation(sessionID, sessionmonitor.ChangeObservation(change, statetrace.OutcomeDiscarded, "store_rejected"))
		return false
	}
	d.recordStateObservation(sessionID, sessionmonitor.ChangeObservation(change, statetrace.OutcomeApplied, ""))
	if opening.Opens && !outcome.HeldBySnooze {
		d.dropEndedSnoozeWake(sessionID, change.State, outcome.EndedSnooze)
		d.enqueueSessionActivity(sessionID)
	}
	if effects.Touch {
		d.store.Touch(sessionID)
	}
	d.kickSessionInbox(sessionID, change.State)
	if effects.Broadcast {
		d.broadcastSessionStateChanged(sessionID)
	}
	return true
}

func (d *Daemon) commitSessionState(sessionID protocol.SessionID, change sessionmonitor.Change, opening store.TurnOpening) (bool, store.TurnOpeningOutcome) {
	if cause, ok := change.Cause.(sessionmonitor.LinkTurn); ok {
		return d.store.ApplyAgentDriverStateOpeningTurn(sessionID, cause.Run, cause.Seq, change.State, change.RequestStartedAt, opening)
	}
	return d.store.UpdateStateOpeningTurn(sessionID, change.State, opening)
}
