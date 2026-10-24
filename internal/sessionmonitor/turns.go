package sessionmonitor

import (
	"errors"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/crashpoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

func (m *Monitor) openTurnLocked(now time.Time, afterNotification bool) bool {
	if !attention.OpensTurn(m.state.State) {
		return false
	}
	requested := !m.turn.RequestedAt.IsZero()
	if afterNotification && !requested && !attention.OpensTurnAfterNotification(m.state.State) {
		return false
	}
	if deadline := m.turn.SnoozedUntil; !deadline.IsZero() {
		if deadline.After(now) && !attention.BreaksSnooze(m.state.State, m.reason) {
			return false
		}
		m.turn.SnoozedUntil = time.Time{}
		m.stopWakeLocked()
	}
	opened := !m.turnOwedLocked()
	if opened {
		m.turn.OpenedAt = now.UTC()
	}
	m.grantTurnRequestLocked()
	return opened
}

func (m *Monitor) grantTurnRequestLocked() {
	if m.turn.RequestedAt.IsZero() || !m.turnOwedLocked() {
		return
	}
	m.turn.AgentAt = m.turn.OpenedAt
	m.turn.RequestedAt = time.Time{}
}

func (m *Monitor) turnOwedLocked() bool {
	return m.turn.OpenedAt.After(m.turn.SettledAt)
}

func (m *Monitor) turnByAgentLocked() bool {
	return attention.TurnByAgent(m.turn.OpenedAt, m.turn.SettledAt, m.turn.AgentAt)
}

func (m *Monitor) settleForAgentLocked(now time.Time) {
	m.turn.SettledAt = now.UTC()
	m.turn.AgentAt = m.turn.SettledAt
	m.stopAutoSettleLocked()
}

func (m *Monitor) agentTurnLocked(action protocol.AgentTurnAction, now time.Time) (protocol.AgentTurnOutcome, error) {
	if action == protocol.AgentTurnActionSettle {
		withdrawn := !m.turn.RequestedAt.IsZero()
		m.turn.RequestedAt = time.Time{}
		switch {
		case m.turnOwedLocked() && !m.turnByAgentLocked():
			return "", errors.New("this turn opened when you stopped, not with attn turn open; only the user settles it")
		case m.turnOwedLocked():
			m.settleForAgentLocked(now)
			return protocol.AgentTurnOutcomeSettled, nil
		case withdrawn:
			return protocol.AgentTurnOutcomeWithdrawn, nil
		}
		return protocol.AgentTurnOutcomeNotOpen, nil
	}
	m.turn.RequestedAt = now.UTC()
	switch {
	case m.turn.SnoozedUntil.After(now):
		return protocol.AgentTurnOutcomeSnoozed, nil
	case !m.turnOwedLocked() && m.state.State == protocol.SessionStateWorking:
		return protocol.AgentTurnOutcomeOpensAtStop, nil
	case !m.turnOwedLocked():
		m.turn.OpenedAt = now.UTC()
	}
	m.grantTurnRequestLocked()
	return protocol.AgentTurnOutcomeOpened, nil
}

func (m *Monitor) settleLocked(turn, now time.Time) bool {
	if !m.turn.OpenedAt.Equal(turn) {
		return false
	}
	m.turn.SettledAt = now.UTC()
	m.stopAutoSettleLocked()
	return true
}

func (m *Monitor) snoozeLocked(turn, until, now time.Time) bool {
	if !m.settleLocked(turn, now) {
		return false
	}
	m.turn.SnoozedUntil = until.UTC()
	m.armWakeLocked()
	return true
}

func (m *Monitor) armWakeLocked() {
	m.stopWakeLocked()
	deadline := m.turn.SnoozedUntil
	if deadline.IsZero() {
		return
	}
	key := m.keyLocked()
	m.wakeTimer = m.registry.host.AfterFunc("monitorSnoozeWake", max(0, time.Until(deadline)), func() {
		crashpoint.At("snooze-wake-running")
		view, woken := m.Do(Command{Key: key, Kind: Wake, Until: deadline}, deadline)
		if woken && m.publish(view) {
			m.traceWake("deadline")
		}
	})
}

func (m *Monitor) wakeLocked(deadline, at time.Time) bool {
	if deadline.IsZero() || !m.turn.SnoozedUntil.Equal(deadline) {
		return false
	}
	m.turn.SnoozedUntil = time.Time{}
	m.stopWakeLocked()
	if attention.OpensTurn(m.state.State) && !m.turn.OpenedAt.After(m.turn.SettledAt) {
		if !at.After(m.turn.SettledAt) {
			at = time.Now()
		}
		m.turn.OpenedAt = at.UTC()
	}
	m.grantTurnRequestLocked()
	return true
}

func (m *Monitor) stopWakeLocked() {
	if m.wakeTimer != nil {
		m.wakeTimer.Stop()
		m.wakeTimer = nil
	}
}

func (m *Monitor) StopWake() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.stopWakeLocked()
	m.mu.Unlock()
}

func (m *Monitor) AgentTurn(action protocol.AgentTurnAction, now time.Time) (protocol.AgentTurnOutcome, error) {
	m.mu.Lock()
	outcome, err := m.agentTurnLocked(action, now)
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
	return outcome, err
}

func (m *Monitor) InNotificationRun() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.input.inNotificationRunLocked()
}

func (m *Monitor) traceWake(cause string) {
	m.mu.Lock()
	state, label := m.state.State, m.label
	m.mu.Unlock()
	if m.registry.host.Debug() {
		m.logf("snooze woken: session=%s cause=%s", label, cause)
	}
	m.Trace(statetrace.Observation{Source: "user", Claim: string(state), Detail: cause, Cause: "wake", Outcome: statetrace.OutcomeApplied})
}
