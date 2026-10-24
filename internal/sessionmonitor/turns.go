package sessionmonitor

import (
	"errors"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

func (e *Entry) openTurnLocked(now time.Time, afterNotification bool) bool {
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

func (e *Entry) grantTurnRequestLocked() {
	if e.turn.RequestedAt.IsZero() || !e.turnOwedLocked() {
		return
	}
	e.turn.AgentAt = e.turn.OpenedAt
	e.turn.RequestedAt = time.Time{}
}

func (e *Entry) turnOwedLocked() bool {
	return e.turn.OpenedAt.After(e.turn.SettledAt)
}

func (e *Entry) turnByAgentLocked() bool {
	return attention.TurnByAgent(e.turn.OpenedAt, e.turn.SettledAt, e.turn.AgentAt)
}

func (e *Entry) settleForAgentLocked(now time.Time) {
	e.turn.SettledAt = now.UTC()
	e.turn.AgentAt = e.turn.SettledAt
	e.stopAutoSettleLocked()
}

func (e *Entry) agentTurnLocked(action protocol.AgentTurnAction, now time.Time) (protocol.AgentTurnOutcome, error) {
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

func (e *Entry) settleLocked(turn, now time.Time) bool {
	if !e.turn.OpenedAt.Equal(turn) {
		return false
	}
	e.turn.SettledAt = now.UTC()
	e.stopAutoSettleLocked()
	return true
}

func (e *Entry) snoozeLocked(turn, until, now time.Time) bool {
	if !e.settleLocked(turn, now) {
		return false
	}
	e.turn.SnoozedUntil = until.UTC()
	e.armWakeLocked()
	return true
}

func (e *Entry) armWakeLocked() {
	e.stopWakeLocked()
	deadline := e.turn.SnoozedUntil
	if deadline.IsZero() {
		return
	}
	key := e.keyLocked()
	e.wakeTimer = e.registry.host.AfterFunc("monitorSnoozeWake", max(0, time.Until(deadline)), func() {
		crashpoint.At("snooze-wake-running")
		view, woken := e.Do(Command{Key: key, Kind: Wake, Until: deadline}, deadline)
		if woken && e.publish(view) {
			e.traceWake("deadline")
		}
	})
}

func (e *Entry) wakeLocked(deadline, at time.Time) bool {
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

func (e *Entry) stopWakeLocked() {
	if e.wakeTimer != nil {
		e.wakeTimer.Stop()
		e.wakeTimer = nil
	}
}

func (e *Entry) StopWake() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.stopWakeLocked()
	e.mu.Unlock()
}

func (e *Entry) AgentTurn(action protocol.AgentTurnAction, now time.Time) (protocol.AgentTurnOutcome, error) {
	e.mu.Lock()
	outcome, err := e.agentTurnLocked(action, now)
	view := e.viewLocked()
	e.mu.Unlock()
	e.publish(view)
	return outcome, err
}

func (e *Entry) InNotificationRun() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.input.inNotificationRunLocked()
}

func (e *Entry) traceWake(cause string) {
	e.mu.Lock()
	state, label := e.state.State, e.label
	e.mu.Unlock()
	if e.registry.host.Debug() {
		e.logf("snooze woken: session=%s cause=%s", label, cause)
	}
	e.Trace(statetrace.Observation{Source: "user", Claim: string(state), Detail: cause, Cause: "wake", Outcome: statetrace.OutcomeApplied})
}
