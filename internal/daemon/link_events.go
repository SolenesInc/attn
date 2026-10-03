package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

// linkEventSink applies what links report. Turn events set state outright, not through
// the resolver, because their epoch and sequence fence is part of the store's state write.
type linkEventSink struct{ daemon *Daemon }

func (d *Daemon) linkEvents() harness.Events { return linkEventSink{daemon: d} }

var linkTurnStates = map[harness.Turn]string{
	harness.TurnUnknown:  protocol.StateUnknown,
	harness.TurnRunning:  protocol.StateWorking,
	harness.TurnApproval: protocol.StatePendingApproval,
	harness.TurnQuestion: protocol.StateWaitingInput,
	harness.TurnEnded:    protocol.StateIdle,
}

func (e linkEventSink) Turn(sessionID string, at time.Time, event harness.TurnEvent) {
	d := e.daemon
	state := linkTurnStates[event.Turn]
	if event.Restated {
		if session := d.store.Get(sessionID); session == nil || session.State != protocol.SessionStateUnknown {
			return
		}
		d.logf("link restates session=%s run=%s as %s after unknown", sessionID, event.Epoch, state)
	}
	var requestStartedAt time.Time
	if event.Turn == harness.TurnRunning {
		requestStartedAt = at
	}
	if !d.applyState(sessionStateChange{
		sessionID:        sessionID,
		state:            state,
		requestStartedAt: requestStartedAt,
		cause:            linkTurn{run: event.Epoch, seq: event.Seq},
		origin:           stateOrigin{source: stateSourceLink, detail: event.Epoch},
	}) {
		d.logf("link turn discarded: session=%s run=%s seq=%d state=%s", sessionID, event.Epoch, event.Seq, state)
	}
}
