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
	session.TurnOpenedAt = nil

	in := d.attentionInputFor(session)
	if !attention.Owed(in) {
		return
	}
	session.TurnOwed = protocol.Ptr(true)
	session.TurnOpenedAt = protocol.Ptr(in.OpenedAt.UTC().Format(time.RFC3339Nano))
}

func (d *Daemon) attentionInputFor(session *protocol.Session) attention.Input {
	stamps := d.store.TurnStamps(session.ID)
	in := attention.Input{
		OpenedAt:     stamps.OpenedAt,
		SettledAt:    stamps.SettledAt,
		IsShell:      string(session.Agent) == protocol.AgentShellValue,
		ChiefOfStaff: protocol.Deref(session.ChiefOfStaff),
	}
	return in
}
