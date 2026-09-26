package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/statetrace"
)

func (d *Daemon) handleSettleTurn(msg *protocol.SettleTurnMessage) {
	if d == nil || d.store == nil || msg == nil {
		return
	}
	sessionID := strings.TrimSpace(msg.SessionID)
	if sessionID == "" {
		return
	}
	if !d.store.SettleTurn(sessionID, time.Now()) {
		return
	}
	d.cancelAutoSettle(sessionID, "settled by user")
	d.traceSettle(sessionID)
	d.broadcastSessionStateChanged(sessionID)
}

func (d *Daemon) handlePinSession(client *wsClient, msg *protocol.PinSessionMessage) {
	if msg == nil {
		return
	}
	if errMsg := d.setSessionPinned(msg.SessionID, msg.Pinned); errMsg != "" {
		d.sendCommandError(client, protocol.CmdPinSession, errMsg)
	}
}

func (d *Daemon) setSessionPinned(sessionID string, pinned bool) string {
	if d == nil || d.store == nil {
		return "store unavailable"
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return "missing session_id"
	}
	session := d.store.Get(id)
	if session == nil {
		return "session not found"
	}
	if d.isChiefOfStaffSession(id) {
		return "the chief of staff is already anchored above the queue"
	}
	alreadyPinned := strings.TrimSpace(protocol.Deref(session.PinnedAt)) != ""
	if alreadyPinned == pinned {
		return ""
	}
	if !d.store.SetSessionPinned(id, pinned, time.Now()) {
		return "persist session pin failed"
	}
	d.publishFact(FactSessionPinChanged, id, nil)
	return ""
}

func (d *Daemon) traceSettle(sessionID string) {
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
		Cause:   "settle",
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
		IsShell:       string(session.Agent) == protocol.AgentShellValue,
		ChiefOfStaff:  protocol.Deref(session.ChiefOfStaff),
		SessionPinned: strings.TrimSpace(protocol.Deref(session.PinnedAt)) != "",
	}
	if workspace := d.store.GetWorkspace(session.WorkspaceID); workspace != nil {
		in.WorkspacePinned = workspace.Pinned
		in.WorkspaceMuted = workspace.Muted
	}
	return in
}
