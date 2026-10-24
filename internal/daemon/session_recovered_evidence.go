package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
)

func (d *Daemon) seedRecoveredEvidence(sessionID protocol.SessionID, existing *protocol.Session, info ptybackend.SessionInfo) {
	if d == nil || existing == nil {
		return
	}
	var seeds []func(*sessionmonitor.Evidence)
	var at time.Time
	if route, ok := d.recoveredApprovalRoute(sessionID); ok {
		inLoop := route.ReviewerInLoop()
		seeds = append(seeds, func(e *sessionmonitor.Evidence) { e.ReviewerInLoop = inLoop })
		at = time.Now()
	}
	if info.HasLastSignal {
		signalAt := info.LastSignal.At
		if signalAt.IsZero() {
			signalAt = time.Now()
		}
		if signal, ok := sessionmonitor.PTYEvidence(info.LastSignal, signalAt); ok && !d.sessionMonitor(sessionID).HoldsPTY(info.LastSignal) {
			seeds = append(seeds, signal)
			at = signalAt
		}
	}
	if edge, concludedAt, ok := recoveredHarnessEdge(existing, info); ok {
		seeds = append(seeds, edge)
		at = concludedAt
	}
	if len(seeds) == 0 {
		return
	}
	d.sessionMonitor(sessionID).RecordEvidence(at, func(e *sessionmonitor.Evidence) {
		for _, seed := range seeds {
			seed(e)
		}
	})
}

func recoveredHarnessEdge(existing *protocol.Session, info ptybackend.SessionInfo) (func(*sessionmonitor.Evidence), time.Time, bool) {
	claim, ok := recoveredHarnessClaim(existing.State)
	if !ok {
		return nil, time.Time{}, false
	}
	concludedAt, ok := parseSessionStateSince(existing)
	if !ok {
		return nil, time.Time{}, false
	}
	if info.HasLastSignal && (recordClaims(info.LastSignal, claim) || info.LastSignal.At.After(concludedAt)) {
		return nil, time.Time{}, false
	}
	return func(e *sessionmonitor.Evidence) {
		e.LastHarnessEvent = &sessionstate.Observation{
			Source:     sessionstate.SourceHarnessEvent,
			Claim:      claim,
			Detail:     "recovered from persisted state",
			ObservedAt: concludedAt,
		}
		e.TurnEverOpened = true
	}, concludedAt, true
}

func recordClaims(obs pty.Observation, wait sessionstate.Claim) bool {
	if obs.Source != pty.SourceProgramStatus {
		return false
	}
	recorded, _ := sessionmonitor.ProgramStatusClaim(obs.Claim)
	return recorded == wait ||
		(recorded == sessionstate.ClaimStopFailed && wait == sessionstate.ClaimNeedsInput) ||
		(recorded == sessionstate.ClaimNeedsInput && wait == sessionstate.ClaimApprovalPending)
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
