package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleSnoozeTurnConn(conn net.Conn, msg *protocol.SnoozeTurnMessage) {
	if err := d.authorizeSnooze(protocol.Deref(msg.CallerSessionID), msg.SessionID, "snooze"); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if err := d.handleSnoozeTurn(msg); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) handleWakeTurnConn(conn net.Conn, msg *protocol.WakeTurnMessage) {
	if err := d.authorizeSnooze(protocol.Deref(msg.CallerSessionID), msg.SessionID, "unsnooze"); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if err := d.handleWakeTurn(msg); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	d.sendOK(conn)
}

func (d *Daemon) authorizeSnooze(callerID, sessionID protocol.SessionID, action string) error {
	sessionID = protocol.TrimID(sessionID)
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("unknown live session %s", sessionID)
	}
	return d.mayActOnSession(protocol.TrimID(callerID), session, action)
}

func (d *Daemon) handleSnoozeTurn(msg *protocol.SnoozeTurnMessage) error {
	sessionID := protocol.TrimID(msg.SessionID)
	if d.store.Get(sessionID) == nil {
		return fmt.Errorf("unknown live session %s", sessionID)
	}
	until, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(msg.Until))
	if err != nil {
		return fmt.Errorf("session %s: until %q is not an RFC3339 instant", sessionID, msg.Until)
	}
	now := time.Now()
	if !until.After(now) {
		return fmt.Errorf("session %s: until %s is in the past; choose a future instant", sessionID, msg.Until)
	}
	entry := d.monitorEntry(sessionID)
	snoozed := false
	if entry != nil {
		turn := d.store.TurnStamps(sessionID).OpenedAt
		view, applied := entry.command(monitorCommand{Key: entry.key(), Kind: monitorSnooze, Turn: turn, Until: until}, now)
		snoozed = applied
		pausepoint.At(pausepoint.SessionSnoozeCommitted)
		if applied && !entry.sink.Applied(view) {
			return fmt.Errorf("session %s: snooze is pending; attn will keep retrying until it is saved", sessionID)
		}
	} else {
		snoozed = d.store.SnoozeTurn(sessionID, until, now)
	}
	if !snoozed {
		return fmt.Errorf("session %s: could not save snooze", sessionID)
	}
	d.cancelAutoSettle(sessionID, "snoozed")
	d.traceSettle(sessionID)
	if entry == nil {
		d.broadcastSessionStateChanged(sessionID)
	}
	return nil
}

func (d *Daemon) handleWakeTurn(msg *protocol.WakeTurnMessage) error {
	sessionID := protocol.TrimID(msg.SessionID)
	if d.store.Get(sessionID) == nil {
		return fmt.Errorf("unknown live session %s", sessionID)
	}
	return d.wakeSnooze(sessionID, time.Now(), "user")
}

func (d *Daemon) wakeSnooze(sessionID protocol.SessionID, at time.Time, cause string) error {
	if entry := d.monitorEntry(sessionID); entry != nil {
		view, accepted := entry.command(monitorCommand{Key: entry.key(), Kind: monitorWake}, at)
		if !accepted {
			return fmt.Errorf("session %s: could not save unsnooze", sessionID)
		}
		if !entry.sink.Applied(view) {
			return fmt.Errorf("session %s: unsnooze is pending; attn will keep retrying until it is saved", sessionID)
		}
		d.finishSnoozeWake(sessionID, at, cause)
		return nil
	}
	deadline := d.store.TurnStamps(sessionID).SnoozedUntil
	if deadline.IsZero() {
		return nil
	}
	if !d.applySnoozeWakeAt(sessionID, deadline, at) {
		if !d.store.TurnStamps(sessionID).SnoozedUntil.IsZero() {
			return fmt.Errorf("session %s: could not save unsnooze", sessionID)
		}
		return nil
	}
	d.finishSnoozeWake(sessionID, at, cause)
	return nil
}

func (d *Daemon) applySnoozeWakeAt(sessionID protocol.SessionID, deadline, at time.Time) bool {
	session := d.store.Get(sessionID)
	if session == nil {
		return false
	}
	if attention.OpensTurn(session.State) {
		return d.store.WakeTurnAtAndOpenIfClosed(sessionID, deadline, d.turnOpensAtOnWake(sessionID, at))
	}
	return d.store.WakeTurnAt(sessionID, deadline)
}

func (d *Daemon) finishSnoozeWake(sessionID protocol.SessionID, at time.Time, cause string) {
	if d.debugLogging {
		d.logf("snooze woken: session=%s cause=%s", sessionID, cause)
	}
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  "user",
		Claim:   d.currentStateClaim(sessionID),
		Detail:  cause,
		Cause:   "wake",
		Outcome: statetrace.OutcomeApplied,
	})
	if d.monitorEntry(sessionID) == nil {
		d.broadcastSessionStateChanged(sessionID)
	}
}

func (d *Daemon) turnOpensAtOnWake(sessionID protocol.SessionID, deadline time.Time) time.Time {
	if deadline.After(d.store.TurnStamps(sessionID).SettledAt) {
		return deadline
	}
	return time.Now()
}

func (d *Daemon) currentStateClaim(sessionID protocol.SessionID) string {
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return string(session.State)
}

func (d *Daemon) turnOpeningFor(sessionID protocol.SessionID, state protocol.SessionState) store.TurnOpening {
	if !attention.OpensTurn(state) {
		return store.TurnOpening{}
	}
	return store.TurnOpening{Opens: true, BreaksSnooze: attention.BreaksSnooze(state, d.stateReason(sessionID))}
}

func (d *Daemon) dropEndedSnoozeWake(sessionID protocol.SessionID, state string, ended time.Time) {
	if ended.IsZero() {
		return
	}
	if d.debugLogging {
		cause := "broken"
		if !ended.After(time.Now()) {
			cause = "expired"
		}
		d.logf("snooze %s: session=%s state=%s reason=%s", cause, sessionID, state, d.stateReason(sessionID))
	}
}

func (d *Daemon) clearSnoozeState(sessionID protocol.SessionID) {
	if entry := d.monitorEntry(sessionID); entry != nil {
		entry.mu.Lock()
		entry.stopWakeLocked()
		entry.mu.Unlock()
	}
}

func (d *Daemon) decorateSessionWithSnooze(session *protocol.Session) {
	if session == nil || session.TurnSnoozedUntil == nil {
		return
	}
	if until, err := time.Parse(time.RFC3339Nano, *session.TurnSnoozedUntil); err != nil || !until.After(time.Now()) {
		session.TurnSnoozedUntil = nil
	}
}
