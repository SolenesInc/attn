package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
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
	result, err := d.monitorCommand(sessionID, "snooze", until, "")
	if err != nil {
		return err
	}
	if !result.Applied {
		return fmt.Errorf("session %s: could not save snooze", sessionID)
	}
	return nil
}

func (d *Daemon) handleWakeTurn(msg *protocol.WakeTurnMessage) error {
	sessionID := protocol.TrimID(msg.SessionID)
	if d.store.Get(sessionID) == nil {
		return fmt.Errorf("unknown live session %s", sessionID)
	}

	result, err := d.monitorCommand(sessionID, "wake", time.Time{}, "")
	if err != nil {
		return err
	}
	if !result.Applied {
		return fmt.Errorf("session %s: could not save unsnooze", sessionID)
	}
	return nil
}

func (d *Daemon) clearSnoozeState(sessionID protocol.SessionID) {
	if monitor := d.sessionMonitor(sessionID); monitor != nil {
		monitor.StopWake()
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
