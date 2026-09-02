package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	agentMessageDedupeWindow = 10 * time.Second
	agentMessageRateWindow   = 30 * time.Second
	agentMessageRateLimit    = 8
	agentMessageQueueCap     = 50
)

var errDoorbellNotTaken = errors.New("doorbell typed but the target did not take it")
var errPeerMessageWaitingForRead = errors.New("an older peer message is waiting to be read")

// An initial-prompt delivery owns the prompt until its submit hook. Anything
// behind it must queue rather than paste into priming or a trust dialog.
var errAgentMessageInitialPromptPending = errors.New("target is still taking its initial prompt")

func agentMessageGuardVerdict(counts agentmailbox.PeerGuardCounts) string {
	switch {
	case counts.DuplicateFromSender:
		return fmt.Sprintf(
			"you already sent that exact text to this session within the last %s; say something new, or wait for a reply",
			agentMessageDedupeWindow)
	case counts.FromSenderInWindow >= agentMessageRateLimit:
		return fmt.Sprintf(
			"rate limit: %d messages per %s to one session, and you have sent %d; slow down",
			agentMessageRateLimit, agentMessageRateWindow, counts.FromSenderInWindow)
	case counts.UnreadForRecipient >= agentMessageQueueCap:
		return fmt.Sprintf(
			"that session has %d unread messages and the queue cap is %d; it has to read some before more arrive",
			counts.UnreadForRecipient, agentMessageQueueCap)
	}
	return ""
}

func (d *Daemon) handleAgentMsg(conn net.Conn, msg *protocol.AgentMsgMessage) {
	sender, errCode := d.resolveSessionByIDOrPrefix(msg.SourceSessionID)
	if sender == nil {
		d.sendError(conn, "sender_"+errCode)
		return
	}

	content := strings.TrimSpace(msg.Content)
	result := &protocol.AgentMsgResult{Status: protocol.AgentMsgStatusRefused}
	switch {
	case content == "":
		result.Detail = "the message is empty; there is nothing to deliver"
	case len(content) > protocol.AgentMessageMaxChars:
		result.Detail = fmt.Sprintf(
			"message is %d bytes and the limit is %d; send the gist and point at the rest",
			len(content), protocol.AgentMessageMaxChars)
	}
	if result.Detail != "" {
		d.replyAgentMsg(conn, result)
		return
	}

	if seedID := strings.TrimSpace(protocol.Deref(msg.TargetSeedID)); seedID != "" {
		if strings.TrimSpace(msg.TargetSessionID) != "" {
			d.replyAgentMsgError(conn, "ambiguous_target",
				"a message goes to one place; name a session or a seed, not both")
			return
		}
		tender, err := d.seedTenderSession(seedID)
		if err != nil {
			d.replyAgentMsgError(conn, "seed_untended", err.Error())
			return
		}
		msg.TargetSessionID = tender
	}

	now := time.Now()
	member, memberFound, memberErr := d.agentMessageMember(msg.TargetSessionID)
	target, targetErrCode := d.resolveSessionByIDOrPrefix(msg.TargetSessionID)
	if memberFound {
		if d.crewBindingLive(member) {
			target = d.store.Get(member.BindingSession)
		} else {
			message := agentmailbox.PeerMessage{
				ID: uuid.NewString(), SenderSessionID: sender.ID, Body: content,
				CreatedAt: now.UTC().Format(time.RFC3339Nano),
			}
			woken, err := d.crewWakeWithDelivery(member.ID, "", true, &crewWakeDelivery{
				Message: &message,
			})
			if err != nil {
				d.sendError(conn, err.Error())
				return
			}
			target = d.store.Get(woken.SessionID)
			if !woken.AlreadyAwake {
				memberName := crew.DisplayName(member.ID)
				result.MessageID = message.ID
				result.TargetSessionID = woken.SessionID
				result.Status = protocol.AgentMsgStatusQueued
				result.Detail = fmt.Sprintf("woke %s in session %s; notification queued until it reaches a safe prompt", memberName, shortSessionID(woken.SessionID))
				d.replyAgentMsg(conn, result)
				return
			}
		}
	}
	if target == nil {
		if memberErr != nil {
			d.sendError(conn, memberErr.Error())
			return
		}
		if targetErrCode == "session_not_found" {
			d.replyAgentMsgError(conn, "session_or_crew_member_not_found", fmt.Sprintf(
				"no session or crew member matches %q; `attn agent list` names sessions and `attn crew list` names members",
				strings.TrimSpace(msg.TargetSessionID)))
			return
		}
		d.sendError(conn, targetErrCode)
		return
	}
	result.TargetSessionID = target.ID
	if sender.ID == target.ID {
		result.Detail = "that is this session; a message to yourself is not a conversation"
		d.replyAgentMsg(conn, result)
		return
	}

	counts, err := d.store.PeerMessageGuardCounts(
		sender.ID, target.ID, content,
		now.Add(-agentMessageDedupeWindow), now.Add(-agentMessageRateWindow),
	)
	if err != nil {
		d.logf("agent msg guard counts: sender=%s target=%s err=%v", sender.ID, target.ID, err)
		d.sendError(conn, "internal_error")
		return
	}
	if verdict := agentMessageGuardVerdict(counts); verdict != "" {
		result.Detail = verdict
		d.replyAgentMsg(conn, result)
		return
	}

	message := agentmailbox.PeerMessage{
		ID: uuid.NewString(), SenderSessionID: sender.ID, Body: content,
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
	}
	delivery, err := d.store.EnqueuePeerMessage(message, target.ID)
	if err != nil {
		d.logf("agent msg enqueue: sender=%s target=%s err=%v", sender.ID, target.ID, err)
		d.sendError(conn, "internal_error")
		return
	}
	d.noteQueuedAgentMailboxItem(target.ID)

	result.MessageID = message.ID
	if err := d.deliverAgentMailboxItem(delivery); err != nil {
		result.Status = protocol.AgentMsgStatusQueued
		result.Detail = agentMessageQueuedDetail(err)
	} else {
		result.Status = protocol.AgentMsgStatusNotified
		targetName := sessionDisplayName(target)
		if memberFound {
			targetName = crew.DisplayName(member.ID)
		}
		result.Detail = fmt.Sprintf("notified %s", targetName)
	}
	d.replyAgentMsg(conn, result)
}

func (d *Daemon) agentMessageMember(address string) (crew.Member, bool, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, false, err
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		return crew.Member{}, false, err
	}
	member, ok := crew.Resolve(address, members)
	return member, ok, nil
}

func (d *Daemon) replyAgentMsg(conn net.Conn, result *protocol.AgentMsgResult) {
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentMsgResult: result})
}

func (d *Daemon) replyAgentMsgError(conn net.Conn, code, message string) {
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: false, Error: protocol.Ptr(message), ErrorCode: protocol.Ptr(code),
	})
}

func agentMessageQueuedDetail(err error) string {
	if errors.Is(err, errPeerMessageWaitingForRead) {
		return "queued (an older message is waiting to be read — this notification lands immediately after it is read)"
	}
	if errors.Is(err, errAgentMessageInitialPromptPending) {
		return "queued (target is waking and still reading its priming — lands immediately after its first prompt starts)"
	}
	if errors.Is(err, errSessionInputBlockedByApproval) {
		return "queued (target is waiting on an approval — lands when the approval clears)"
	}
	if errors.Is(err, errSessionInputBlockedBySelector) {
		return "queued (target's screen is waiting on a keypress, so typed words would answer it — lands once that clears)"
	}
	if errors.Is(err, errSessionInputComposerDirty) {
		return "queued (the user typed in the target moments ago; lands once the composer has been quiet for a while)"
	}
	if errors.Is(err, errSessionInputScreenUnavailable) {
		return "queued (attn cannot see a safe prompt on the target yet; lands on its next state change)"
	}
	if errors.Is(err, errDoorbellNotTaken) {
		return fmt.Sprintf(
			"queued (typed it, but the target did not start a turn within %s — something in front of its prompt ate it; lands on its next state change)",
			sessionInputTakenWindow)
	}
	return "queued (target is not taking input right now — lands when it is running again; don't wait for a reply)"
}

func (d *Daemon) composePeerMessageDoorbell(sender *protocol.Session, message agentmailbox.PeerMessage) string {
	shortID := shortSessionID(message.SenderSessionID)
	origin := shortID
	if sender != nil {
		origin = fmt.Sprintf("%s (%s)", shortID, d.sessionOriginName(sender))
	}
	return fmt.Sprintf("📨 session %s sent message %s — read it with `attn agent inbox %s`.",
		origin, message.ID, message.ID)
}

func (d *Daemon) sessionOriginName(session *protocol.Session) string {
	if workspace := d.store.GetWorkspace(session.WorkspaceID); workspace != nil && strings.TrimSpace(workspace.Title) != "" {
		return workspace.Title
	}
	return sessionDisplayName(session)
}

func sessionDisplayName(session *protocol.Session) string {
	if label := strings.TrimSpace(session.Label); label != "" {
		return label
	}
	return shortSessionID(session.ID)
}

func shortSessionID(id string) string {
	if len(id) <= agentShortIDLength {
		return id
	}
	return id[:agentShortIDLength]
}

func (d *Daemon) seedTenderSession(seedID string) (string, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return "", err
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return "", err
	}
	tender := seed.Tender()
	if session := strings.TrimSpace(tender.Session); session != "" {
		return session, nil
	}
	if tender.Named() {
		return "", fmt.Errorf(
			"%s is tended by %s, who is not in an attn session; message them by name: attn agent msg %s \"…\"",
			seed.ID, tender.DisplayName(), tender.Name())
	}
	return "", fmt.Errorf(
		"nobody is tending %s, so there is nobody to reach; leave it on the log instead: attn seed note %s -m \"…\"",
		seed.ID, seed.ID)
}
