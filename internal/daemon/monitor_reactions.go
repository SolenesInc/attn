package daemon

import (
	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

func (d *Daemon) reactMonitorView(session protocol.SessionID, previous, view monitorView) {
	state, turn := view.State.V, view.Turn.V
	stateChanged := state.Sequence != previous.State.V.Sequence
	if view.LastModelRequestAt.V != previous.LastModelRequestAt.V && !stateChanged {
		d.publishFact(FactSessionModelRequestStarted, string(session), nil)
	}
	if previous.FirstReportAt.V.IsZero() && !view.FirstReportAt.V.IsZero() {
		d.noteLaunchStarted(session)
	}
	if turn.OpenedAt.After(previous.Turn.V.OpenedAt) || (stateChanged && attention.OpensTurn(state.State) && turn.SnoozedUntil.IsZero()) {
		d.enqueueSessionActivity(session)
	}
	if stateChanged && !state.Quiet {
		d.updateTranscriptWatcherState(session, state.State)
		source := state.Origin.source
		if source == "" {
			source = state.Cause
		}
		d.recordStateObservation(session, statetrace.Observation{
			Source: source, Claim: string(state.State), Detail: state.Origin.detail,
			Cause: state.Cause, Outcome: statetrace.OutcomeApplied, ObservedAt: state.Origin.observedAt,
		})
		if state.Touch {
			d.store.Touch(session)
		}
		d.kickSessionInbox(session, string(state.State))
		if state.Broadcast {
			d.broadcastSessionStateChanged(session)
		}
		if state.Resolve {
			d.resolveSoon(session)
		}
	}
	if !stateChanged && (turn != previous.Turn.V || view.AutoSettle.V != previous.AutoSettle.V || state.Reason != previous.State.V.Reason) {
		d.broadcastSessionStateChanged(session)
	}
}
