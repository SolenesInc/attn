package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionstate"
)

// The bracket pair (turn/tool open) is deliberately NOT reconstructed on recovery:
// inventing it would hold a session `working` on a bracket whose closing hook can never arrive.

func (d *Daemon) seedRecoveredEvidence(sessionID string, existing *protocol.Session, info ptybackend.SessionInfo) {
	if d == nil || existing == nil {
		return
	}
	if route, ok := d.recoveredApprovalRoute(sessionID); ok {
		d.recordReviewerEvidence(sessionID, route.ReviewerInLoop())
	}
	if info.HasLastSignal {
		d.recordPTYEvidence(sessionID, info.LastSignal)
	}
	d.seedRecoveredHarnessEdge(sessionID, existing, info)
}

// A title painted after the state was concluded means the prompt was answered unobserved,
// so only a level no newer than the conclusion corroborates the edge.
func (d *Daemon) seedRecoveredHarnessEdge(sessionID string, existing *protocol.Session, info ptybackend.SessionInfo) {
	claim, ok := recoveredHarnessClaim(existing.State)
	if !ok {
		return
	}
	concludedAt, ok := parseSessionStateSince(existing)
	if !ok {
		return
	}
	if info.HasLastSignal && info.LastSignal.At.After(concludedAt) {
		return
	}
	d.recordEvidence(sessionID, concludedAt, func(e *sessionstate.Evidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      claim,
			Detail:     "recovered from persisted state",
			ObservedAt: concludedAt,
		}
		e.TurnEverOpened = true
	})
}

func recoveredHarnessClaim(state protocol.SessionState) (sessionstate.Claim, bool) {
	switch state {
	case protocol.SessionStatePendingApproval:
		return sessionstate.ClaimApprovalPending, true
	case protocol.SessionStateWaitingInput:
		return sessionstate.ClaimNeedsInput, true
	default:
		return "", false
	}
}

func parseSessionStateSince(session *protocol.Session) (time.Time, bool) {
	stamp, err := time.Parse(time.RFC3339Nano, session.StateSince)
	if err != nil {
		return time.Time{}, false
	}
	return stamp, true
}
