package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const agentCloseReasonMaxChars = garden.MaxReasonChars

const agentCloseTendedSeedLimit = 100

func (d *Daemon) handleAgentClose(conn net.Conn, msg *protocol.AgentCloseMessage) {
	b, err := d.bindings()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	r, err := d.requestFromSession(msg.SourceSessionID, b)
	if err != nil {
		d.replyTargetError(conn, err)
		return
	}
	asking, _ := r.AskingSession()
	caller, code := d.resolveSessionByIDOrPrefix(string(asking), r.ProfileID())
	if caller == nil {
		d.replyAgentMsgError(conn, "sender_"+code, "the caller's session has ended")
		return
	}

	reason := strings.TrimSpace(msg.Reason)
	switch {
	case reason == "":
		d.replyAgentMsgError(conn, "close_reason_required",
			"a close needs a reason: the ledger keeps the row, and the reason is all the next reader gets")
		return
	case utf8.RuneCountInString(reason) > agentCloseReasonMaxChars:
		d.replyAgentMsgError(conn, "close_reason_required", fmt.Sprintf(
			"that reason is %d characters and the limit is %d; say why it is done and put the detail on the seed",
			utf8.RuneCountInString(reason), agentCloseReasonMaxChars))
		return
	}

	target, err := d.resolveSession(r, b, msg.To)
	if err != nil {
		d.replyTargetError(conn, err)
		return
	}

	if err := d.mayActOnSession(caller.ID, target, "close"); err != nil {
		d.replyAgentMsgError(conn, "close_not_authorized", err.Error())
		return
	}
	rule := protocol.AgentCloseRuleDispatcher
	if caller.ID == target.ID {
		rule = protocol.AgentCloseRuleSelf
	} else if d.requestedByChief(r) {
		rule = protocol.AgentCloseRuleChief
	}
	if protectErr := d.sessionCloseError(target.ID); protectErr != nil {
		d.replyAgentMsgError(conn, "session_close_protected", protectErr.Error())
		return
	}

	d.logf("agent close: session %s closes %s as %s: %s", caller.ID, target.ID, rule, reason)
	closing, err := d.beginSessionClose(target.ID, store.SessionClose{By: r.Actor(), Reason: reason}, nil)
	if err != nil {
		d.replyAgentMsgError(conn, "close_failed", fmt.Sprintf(
			"session %s is still running: %v", shortSessionID(target.ID), err))
		return
	}

	result := &protocol.AgentCloseResult{
		TargetSessionID: target.ID,
		Label:           sessionDisplayName(target),
		Reason:          reason,
		Rule:            rule,
		SeedIds:         d.noteCloseOnTendedSeeds(target, caller, rule, reason, r.Actor()),
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentCloseResult: result})
	d.finishSessionClose(target.ID, closing)
}

func (d *Daemon) noteCloseOnTendedSeeds(
	target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string, author who.Actor,
) []string {
	noted := []string{}
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Filters:    []docstore.Filter{{Field: "tender", Op: docstore.OpEq, Value: who.PartyOfEndedSession(target.ID).String()}},
		Limit:      agentCloseTendedSeedLimit,
	})
	if err != nil {
		d.logf("agent close: reading the seeds %s tended: %v", target.ID, err)
		return noted
	}
	body := agentCloseSeedNote(target, caller, rule, reason)
	for _, doc := range read.Documents {
		if _, err := d.appendSeedNote(doc.ID, body, author, garden.NoteKindNote, nil, false); err != nil {
			d.logf("agent close: noting the close of %s on %s: %v", target.ID, doc.ID, err)
			continue
		}
		noted = append(noted, doc.ID)
	}
	return noted
}

func agentCloseSessionRef(session *protocol.Session) string {
	if label := strings.TrimSpace(session.Label); label != "" {
		return fmt.Sprintf("%s (%s)", label, shortSessionID(session.ID))
	}
	return shortSessionID(session.ID)
}

func agentCloseSeedNote(target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string) string {
	closer := agentCloseSessionRef(caller)
	switch rule {
	case protocol.AgentCloseRuleSelf:
		closer = "itself"
	case protocol.AgentCloseRuleChief:
		closer = "the Chief, " + closer
	case protocol.AgentCloseRuleDispatcher:
		closer = "its dispatcher, " + closer
	}
	return fmt.Sprintf(
		"Session %s was closed by %s while tending this seed. Reason: %s\n\n"+
			"The seed did not move. It still names that session as its tender, so whoever comes next "+
			"takes it or parks it. `attn session show %s` reads the closed row back.",
		agentCloseSessionRef(target), closer, reason, shortSessionID(target.ID))
}
