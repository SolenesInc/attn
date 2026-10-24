package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/statetrace"
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
	if entry == nil {
		return fmt.Errorf("session %s: no monitor runs it, so its turn can't be snoozed", sessionID)
	}
	turn := d.store.TurnStamps(sessionID).OpenedAt
	view, applied := entry.Do(sessionmonitor.Command{Key: entry.Key(), Kind: sessionmonitor.Snooze, Turn: turn, Until: until}, now)
	pausepoint.At(pausepoint.SessionSnoozeCommitted)
	if !applied {
		return fmt.Errorf("session %s: could not save snooze", sessionID)
	}
	if !entry.Publish(view) {
		return fmt.Errorf("session %s: snooze is pending; attn will keep retrying until it is saved", sessionID)
	}
	entry.CancelAutoSettle("snoozed")
	d.traceSettle(sessionID)
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
	entry := d.monitorEntry(sessionID)
	if entry == nil {
		return fmt.Errorf("session %s: no monitor runs it, so its turn can't be woken", sessionID)
	}
	view, accepted := entry.Do(sessionmonitor.Command{Key: entry.Key(), Kind: sessionmonitor.Wake}, at)
	if !accepted {
		return fmt.Errorf("session %s: could not save unsnooze", sessionID)
	}
	if !entry.Publish(view) {
		return fmt.Errorf("session %s: unsnooze is pending; attn will keep retrying until it is saved", sessionID)
	}
	d.finishSnoozeWake(sessionID, at, cause)
	return nil
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

func (d *Daemon) currentStateClaim(sessionID protocol.SessionID) string {
	session := d.store.Get(sessionID)
	if session == nil {
		return ""
	}
	return string(session.State)
}

func (d *Daemon) clearSnoozeState(sessionID protocol.SessionID) {
	if entry := d.monitorEntry(sessionID); entry != nil {
		entry.StopWake()
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
