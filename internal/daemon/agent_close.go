package daemon

import (
	"context"
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
	result, closing, refusal := d.beginAgentClose(msg)
	if refusal != nil {
		d.replyAgentMsgError(conn, refusal.code, refusal.message)
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentCloseResult: result})
	if !protocol.Deref(result.PendingCompact) {
		d.finishSessionClose(result.TargetSessionID, closing)
	}
}

func (d *Daemon) beginAgentClose(msg *protocol.AgentCloseMessage) (*protocol.AgentCloseResult, sessionCloseInFlight, *agentCloseRefusal) {
	caller, errCode := d.resolveSessionByIDOrPrefix(string(msg.SourceSessionID))
	if caller == nil {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"sender_" + errCode, fmt.Sprintf(
			"the caller %q is not a session on this daemon; a close is attributed to the session that asked for it", protocol.TrimID(msg.SourceSessionID))}
	}

	reason := strings.TrimSpace(msg.Reason)
	switch {
	case reason == "":
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_reason_required",
			"a close needs a reason: the ledger keeps the row, and the reason is all the next reader gets"}
	case utf8.RuneCountInString(reason) > agentCloseReasonMaxChars:
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_reason_required", fmt.Sprintf(
			"that reason is %d characters and the limit is %d; say why it is done and put the detail on the seed",
			utf8.RuneCountInString(reason), agentCloseReasonMaxChars)}
	}

	target, refusal := d.resolveAgentCloseTarget(msg, caller)
	if refusal != nil {
		return nil, sessionCloseInFlight{}, refusal
	}

	if dispatch, ok := d.gardenDispatch(target.ID); caller.ID != target.ID && ok && strings.TrimSpace(dispatch.Crown) != "" {
		if err := d.requireSeedInProfile(dispatch.Crown, caller.ProfileID, false); err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"cross_profile", err.Error()}
		}
	}

	if err := d.mayActOnSession(caller.ID, target, "close"); err != nil {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_not_authorized", err.Error()}
	}
	rule := protocol.AgentCloseRuleDispatcher
	if caller.ID == target.ID {
		rule = protocol.AgentCloseRuleSelf
	} else if d.chiefOfProfile(target.ProfileID) == caller.ID {
		rule = protocol.AgentCloseRuleChiefOfStaff
	}
	compact := protocol.Deref(msg.Compact)
	if compact && rule != protocol.AgentCloseRuleSelf {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"compact_requires_self",
			"--compact compacts the conversation of the session that closes itself, so it only goes with --self"}
	}
	if compact {
		if refusal := d.compactPathRefusal(target); refusal != nil {
			return nil, sessionCloseInFlight{}, refusal
		}
	}
	if rule == protocol.AgentCloseRuleSelf {
		if err := d.unreadStandbyMailRefusal(target, ""); err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"standby_mail_unread", err.Error()}
		}
	}
	if protectErr := d.sessionCloseError(target.ID); protectErr != nil {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"session_close_protected", protectErr.Error()}
	}

	if compact {
		onStandby, err := d.store.SeedsOnStandbyTendedBy(target.ProfileID, d.partyOfSession(target.ID))
		if err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_failed", err.Error()}
		}
		if _, err := Call[struct{}](context.Background(), d.router(), protocol.CompactingCloseBeginMessage{Cmd: protocol.CmdCompactingCloseBegin, SessionID: target.ID, Reason: reason, OnStandby: onStandby}); err != nil {
			return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_failed", err.Error()}
		}
		return &protocol.AgentCloseResult{
			TargetSessionID: target.ID,
			Label:           sessionDisplayName(target),
			Reason:          reason,
			Rule:            rule,
			SeedIds:         []string{},
			PendingCompact:  protocol.Ptr(true),
		}, sessionCloseInFlight{}, nil
	}

	d.logf("agent close: session %s closes %s as %s: %s", caller.ID, target.ID, rule, reason)
	closing, err := d.beginSessionClose(target.ID, store.SessionClose{By: who.Session(caller.ID).Actor(), Reason: reason}, nil)
	if err != nil {
		return nil, sessionCloseInFlight{}, &agentCloseRefusal{"close_failed", fmt.Sprintf(
			"session %s is still running: %v", shortSessionID(target.ID), err)}
	}

	return &protocol.AgentCloseResult{
		TargetSessionID: target.ID,
		Label:           sessionDisplayName(target),
		Reason:          reason,
		Rule:            rule,
		SeedIds:         d.noteCloseOnTendedSeeds(target, caller, rule, reason),
	}, closing, nil
}

type agentCloseRefusal struct {
	code    string
	message string
}

func (d *Daemon) resolveAgentCloseTarget(msg *protocol.AgentCloseMessage, caller *protocol.Session) (*protocol.Session, *agentCloseRefusal) {
	reference := protocol.SessionID(strings.TrimSpace(msg.TargetSessionID))
	if seedID := strings.TrimSpace(protocol.Deref(msg.TargetSeedID)); seedID != "" {
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			return nil, &agentCloseRefusal{"cross_profile", err.Error()}
		}
		if seed.Tender != d.partyOfSession(caller.ID) {
			if err := d.requireSeedInProfile(seedID, caller.ProfileID, false); err != nil {
				return nil, &agentCloseRefusal{"cross_profile", err.Error()}
			}
		}
		if reference != "" {
			return nil, &agentCloseRefusal{"ambiguous_target",
				"a close ends one session; name a session or a seed, not both"}
		}
		tender, err := d.seedTenderSession(seedID)
		if err != nil {
			return nil, &agentCloseRefusal{"seed_untended", err.Error()}
		}
		reference = protocol.SessionID(tender)
	}
	target, errCode := d.resolveAgentCloseSession(reference)
	switch {
	case target != nil:
		return target, nil
	case errCode == "ambiguous_session":
		return nil, &agentCloseRefusal{errCode, fmt.Sprintf(
			"%q matches more than one session; give more of the id (`attn agent list --json` carries full ids)", reference)}
	default:
		return nil, &agentCloseRefusal{errCode, fmt.Sprintf(
			"no session matches %q; `attn agent list` names the sessions this daemon can reach", reference)}
	}
}

func (d *Daemon) resolveAgentCloseSession(reference protocol.SessionID) (*protocol.Session, string) {
	reference = protocol.TrimID(reference)
	if reference == "" {
		return nil, "session_not_found"
	}
	if session := d.store.Get(reference); session != nil {
		return session, ""
	}
	var match *protocol.Session
	for _, session := range d.store.List("") {
		if !strings.HasPrefix(string(session.ID), string(reference)) {
			continue
		}
		if match != nil && match.ID != session.ID {
			return nil, "ambiguous_session"
		}
		match = session
	}
	if match == nil {
		return nil, "session_not_found"
	}
	return match, ""
}

func (d *Daemon) noteCloseOnTendedSeeds(
	target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string,
) []string {
	noted := []string{}
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  garden.Namespace,
		Collection: garden.CollectionSeeds,
		Filters:    []docstore.Filter{{Field: "tender", Op: docstore.OpEq, Value: who.Session(target.ID).String()}, {Field: "profile_id", Op: docstore.OpEq, Value: caller.ProfileID}},
		Limit:      agentCloseTendedSeedLimit,
	})
	if err != nil {
		d.logf("agent close: reading the seeds %s tended: %v", target.ID, err)
		return noted
	}
	for _, doc := range read.Documents {
		standby, err := d.store.SeedStandby(doc.ID)
		if err != nil {
			d.logf("agent close: reading the standby of %s: %v", doc.ID, err)
		}
		onStandby := standby != nil && standby.Tender == who.Session(target.ID)
		body := agentCloseSeedNote(target, caller, rule, reason, onStandby)
		if _, err := d.appendSeedNote(doc.ID, body, d.actorOfSession(caller.ID), garden.NoteKindNote, nil, false, caller.ID); err != nil {
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

func agentCloseSeedNote(target, caller *protocol.Session, rule protocol.AgentCloseRule, reason string, onStandby bool) string {
	closer := agentCloseSessionRef(caller)
	switch rule {
	case protocol.AgentCloseRuleSelf:
		closer = "itself"
	case protocol.AgentCloseRuleChiefOfStaff:
		closer = "the chief of staff, " + closer
	case protocol.AgentCloseRuleDispatcher:
		closer = "its dispatcher, " + closer
	}
	next := "so whoever comes next takes it or parks it"
	if onStandby {
		next = "and the seed is on standby, so its next seed mail reopens that session"
	}
	return fmt.Sprintf(
		"Session %s was closed by %s while tending this seed. Reason: %s\n\n"+
			"The seed did not move. It still names that session as its tender, %s. "+
			"`attn session show %s` reads the closed row back.",
		agentCloseSessionRef(target), closer, reason, next, shortSessionID(target.ID))
}

func (d *Daemon) seedTenderSession(seedID string) (string, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return "", err
	}
	if session, ok := seed.Tender.Session(); ok {
		return string(session), nil
	}
	if session, ok := d.liveSessionOf(seed.Tender); ok {
		return string(session), nil
	}
	if !seed.Tender.IsZero() {
		name := d.partyName(seed.Tender)
		return "", fmt.Errorf(
			"%s is tended by %s, who is not in an attn session; message them by name: attn agent msg %s \"…\"",
			seed.ID, name, name)
	}
	return "", fmt.Errorf(
		"nobody is tending %s, so there is nobody to reach; leave it on the log instead: attn seed note %s -m \"…\"",
		seed.ID, seed.ID)
}
