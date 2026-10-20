package daemon

import (
	"errors"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
)

func (e *monitorEntry) openTurnLocked(now time.Time, afterNotification bool) bool {
	if !attention.OpensTurn(e.state.State) {
		return false
	}
	requested := !e.turn.RequestedAt.IsZero()
	if afterNotification && !requested && !attention.OpensTurnAfterNotification(e.state.State) {
		return false
	}
	if deadline := e.turn.SnoozedUntil; !deadline.IsZero() {
		if deadline.After(now) && !attention.BreaksSnooze(e.state.State, e.reason) {
			return false
		}
		e.turn.SnoozedUntil = time.Time{}
		e.stopWakeLocked()
	}
	opened := !e.turnOwedLocked()
	if opened {
		e.turn.OpenedAt = now.UTC()
	}
	e.grantTurnRequestLocked()
	return opened
}

func (e *monitorEntry) grantTurnRequestLocked() {
	if e.turn.RequestedAt.IsZero() || !e.turnOwedLocked() {
		return
	}
	e.turn.AgentAt = e.turn.OpenedAt
	e.turn.RequestedAt = time.Time{}
}

func (e *monitorEntry) turnOwedLocked() bool {
	return e.turn.OpenedAt.After(e.turn.SettledAt)
}

func (e *monitorEntry) turnByAgentLocked() bool {
	return attention.TurnByAgent(e.turn.OpenedAt, e.turn.SettledAt, e.turn.AgentAt)
}

func (e *monitorEntry) settleForAgentLocked(now time.Time) {
	e.turn.SettledAt = now.UTC()
	e.turn.AgentAt = e.turn.SettledAt
	e.stopAutoSettleLocked()
}

func (e *monitorEntry) agentTurnLocked(action protocol.AgentTurnAction, now time.Time) (protocol.AgentTurnOutcome, error) {
	if action == protocol.AgentTurnActionSettle {
		withdrawn := !e.turn.RequestedAt.IsZero()
		e.turn.RequestedAt = time.Time{}
		switch {
		case e.turnOwedLocked() && !e.turnByAgentLocked():
			return "", errors.New("this turn opened when you stopped, not with attn turn open; only the user settles it")
		case e.turnOwedLocked():
			e.settleForAgentLocked(now)
			return protocol.AgentTurnOutcomeSettled, nil
		case withdrawn:
			return protocol.AgentTurnOutcomeWithdrawn, nil
		}
		return protocol.AgentTurnOutcomeNotOpen, nil
	}
	e.turn.RequestedAt = now.UTC()
	switch {
	case e.turn.SnoozedUntil.After(now):
		return protocol.AgentTurnOutcomeSnoozed, nil
	case !e.turnOwedLocked() && e.state.State == protocol.SessionStateWorking:
		return protocol.AgentTurnOutcomeOpensAtStop, nil
	case !e.turnOwedLocked():
		e.turn.OpenedAt = now.UTC()
	}
	e.grantTurnRequestLocked()
	return protocol.AgentTurnOutcomeOpened, nil
}

func (e *monitorEntry) settleLocked(turn, now time.Time) bool {
	if !e.turn.OpenedAt.Equal(turn) {
		return false
	}
	e.turn.SettledAt = now.UTC()
	e.stopAutoSettleLocked()
	return true
}

func (e *monitorEntry) snoozeLocked(turn, until, now time.Time) bool {
	if !e.settleLocked(turn, now) {
		return false
	}
	e.turn.SnoozedUntil = until.UTC()
	e.armWakeLocked()
	return true
}

func (e *monitorEntry) armWakeLocked() {
	e.stopWakeLocked()
	if !e.turn.SnoozedUntil.IsZero() {
		e.wakeTimer = e.wake(e.turn.SnoozedUntil)
	}
}

func (e *monitorEntry) wakeLocked(deadline, at time.Time) bool {
	if deadline.IsZero() || !e.turn.SnoozedUntil.Equal(deadline) {
		return false
	}
	e.turn.SnoozedUntil = time.Time{}
	e.stopWakeLocked()
	if attention.OpensTurn(e.state.State) && !e.turn.OpenedAt.After(e.turn.SettledAt) {
		if !at.After(e.turn.SettledAt) {
			at = time.Now()
		}
		e.turn.OpenedAt = at.UTC()
	}
	e.grantTurnRequestLocked()
	return true
}

func (e *monitorEntry) stopWakeLocked() {
	if e.wakeTimer != nil {
		e.wakeTimer.Stop()
		e.wakeTimer = nil
	}
}
