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
	caller := d.store.Get(asking)

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

	if dispatch, ok := d.gardenDispatch(target.ID); caller.ID != target.ID && ok && strings.TrimSpace(dispatch.Crown) != "" {
		if err := d.requireSeedInProfile(dispatch.Crown, caller.ProfileID, false); err != nil {
			d.replyAgentMsgError(conn, "cross_profile", err.Error())
			return
		}
	}

	rule, err := d.agentCloseRule(caller, target)
	if err != nil {
		d.replyAgentMsgError(conn, "close_not_authorized", err.Error())
		return
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
		SeedIds:         d.noteCloseOnTendedSeeds(target, caller, rule, reason),
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentCloseResult: result})
	d.finishSessionClose(target.ID, closing)
}

func (d *Daemon) agentCloseCandidates() []*protocol.Session {
	candidates := append([]*protocol.Session(nil), d.store.List("")...)
	if d.hubManager == nil {
		return candidates
	}
	for _, remote := range d.hubManager.RemoteSessions() {
		candidates = append(candidates, &remote)
	}
	return candidates
}

func (d *Daemon) agentCloseRule(caller, target *protocol.Session) (protocol.AgentCloseRule, error) {
	if caller.ID == target.ID {
		return protocol.AgentCloseRuleSelf, nil
	}
	if d.chiefOfProfile(target.ProfileID) == caller.ID || (target.ProfileID == "" && d.isChiefOfStaffSession(caller.ID)) {
		return protocol.AgentCloseRuleChiefOfStaff, nil
	}
	var dispatcher protocol.SessionID
	if dispatch, ok := d.gardenDispatch(target.ID); ok {
		dispatcher = protocol.TrimID(dispatch.DispatcherSession)
	}
	if dispatcher != "" && dispatcher == caller.ID {
		return protocol.AgentCloseRuleDispatcher, nil
	}
	const rules = "a session may close itself and the sessions it dispatched, and a profile's chief of staff may close any agent of that profile"
	if dispatcher == "" {
		return "", fmt.Errorf("%s. Session %s was not dispatched by anyone, so only it and the chief of staff can close it",
			rules, shortSessionID(target.ID))
	}
	return "", fmt.Errorf("%s. Session %s was dispatched by session %s, not by you",
		rules, shortSessionID(target.ID), shortSessionID(dispatcher))
}

func (d *Daemon) noteCloseOnTendedSeeds(
	target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string,
) []string {
	noted := []string{}
	if err := d.requireHome(garden.Surface); err != nil {
		return noted
	}
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Filters:    []docstore.Filter{{Field: "tender_session", Op: docstore.OpEq, Value: string(target.ID)}, {Field: "profile_id", Op: docstore.OpEq, Value: caller.ProfileID}},
		Limit:      agentCloseTendedSeedLimit,
	})
	if err != nil {
		d.logf("agent close: reading the seeds %s tended: %v", target.ID, err)
		return noted
	}
	body := agentCloseSeedNote(target, caller, rule, reason)
	for _, doc := range read.Documents {
		if _, err := d.appendSeedNote(doc.ID, body, caller.ID, "", garden.NoteKindNote, nil, false, caller.ID); err != nil {
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
	case protocol.AgentCloseRuleChiefOfStaff:
		closer = "the chief of staff, " + closer
	case protocol.AgentCloseRuleDispatcher:
		closer = "its dispatcher, " + closer
	}
	return fmt.Sprintf(
		"Session %s was closed by %s while tending this seed. Reason: %s\n\n"+
			"The seed did not move. It still names that session as its tender, so whoever comes next "+
			"takes it or parks it. `attn session show %s` reads the closed row back.",
		agentCloseSessionRef(target), closer, reason, shortSessionID(target.ID))
}
