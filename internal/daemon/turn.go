package daemon

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

func (d *Daemon) handleSettleTurn(msg *protocol.SettleTurnMessage) error {
	if msg == nil {
		return nil
	}
	return d.closeTurn(protocol.TrimID(msg.SessionID), sessionmonitor.Settle)
}

func (d *Daemon) handBackTurn(sessionID protocol.SessionID) {
	if err := d.closeTurn(sessionID, sessionmonitor.HandBack); err != nil {
		d.logf("hand back turn: %v", err)
	}
}

func (d *Daemon) closeTurn(sessionID protocol.SessionID, kind sessionmonitor.CommandKind) error {
	if d == nil || d.store == nil || sessionID == "" {
		return nil
	}
	names := map[sessionmonitor.CommandKind]string{sessionmonitor.Settle: "settle", sessionmonitor.HandBack: "hand_back"}
	_, err := d.monitorCommand(sessionID, names[kind], time.Time{}, "")
	return err
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
		IsShell: session.Agent == protocol.AgentShellValue,
		Chief:   protocol.Deref(session.Chief),
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
