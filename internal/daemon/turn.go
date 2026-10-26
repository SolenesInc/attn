package daemon

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/statetrace"
)

func (d *Daemon) handleSettleTurn(msg *protocol.SettleTurnMessage) {
	if msg == nil {
		return
	}
	d.closeTurn(protocol.TrimID(msg.SessionID), sessionmonitor.Settle, "settle")
}

func (d *Daemon) handBackTurn(sessionID protocol.SessionID) {
	d.closeTurn(sessionID, sessionmonitor.HandBack, "ask answer")
}

func (d *Daemon) closeTurn(sessionID protocol.SessionID, kind sessionmonitor.CommandKind, cause string) {
	if d == nil || d.store == nil || sessionID == "" {
		return
	}
	monitor := d.sessionMonitor(sessionID)
	if monitor == nil {
		return
	}
	turn := d.store.TurnStamps(sessionID).OpenedAt
	view, applied := monitor.Do(sessionmonitor.Command{Key: monitor.Key(), Kind: kind, Turn: turn}, time.Now())
	if !applied || !monitor.Publish(view) {
		return
	}
	monitor.CancelAutoSettle("settled by user")
	d.traceSettle(sessionID, cause)
}

func (d *Daemon) traceSettle(sessionID protocol.SessionID, cause string) {
	session := d.store.Get(sessionID)
	if session == nil {
		return
	}
	reason := ""
	if session.StateReason != nil {
		reason = *session.StateReason
	}
	d.recordStateObservation(sessionID, statetrace.Observation{
		Source:  "user",
		Claim:   string(session.State),
		Detail:  reason,
		Cause:   cause,
		Outcome: statetrace.OutcomeApplied,
	})
}

func (d *Daemon) decorateSessionWithTurn(session *protocol.Session) {
	if session == nil || d.store == nil {
		return
	}
	session.TurnOwed = nil
	if session.TurnOpenedAt == nil || attention.Excluded(d.attentionInputFor(session)) {
		session.TurnOpenedAt = nil
		return
	}
	session.TurnOwed = protocol.Ptr(true)
}

func (d *Daemon) attentionInputFor(session *protocol.Session) attention.Input {
	in := attention.Input{
		IsShell:      session.Agent == protocol.AgentShellValue,
		ChiefOfStaff: protocol.Deref(session.ChiefOfStaff),
	}
	return in
}

func (d *Daemon) setSessionPriority(msg *protocol.SetSessionPriorityMessage) error {
	sessionID := protocol.TrimID(msg.SessionID)
	if !d.store.SetSessionPriority(sessionID, msg.Priority) {
		return fmt.Errorf("cannot set priority for session %s: no open session updated", sessionID)
	}
	d.broadcastSessionStateChanged(sessionID)
	return nil
}
