package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/attention"
)

func (e *monitorEntry) openTurnLocked(now time.Time) bool {
	if !attention.OpensTurn(e.state.State) {
		return false
	}
	if deadline := e.turn.SnoozedUntil; !deadline.IsZero() {
		if deadline.After(now) && !attention.BreaksSnooze(e.state.State, e.reason) {
			return false
		}
		e.turn.SnoozedUntil = time.Time{}
		e.stopWakeLocked()
	}
	if e.turn.OpenedAt.After(e.turn.SettledAt) {
		return false
	}
	e.turn.OpenedAt = now.UTC()
	return true
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
	return true
}

func (e *monitorEntry) stopWakeLocked() {
	if e.wakeTimer != nil {
		e.wakeTimer.Stop()
		e.wakeTimer = nil
	}
}
