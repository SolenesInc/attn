package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	agentMessageDedupeWindow = 10 * time.Second
	agentMessageRateWindow   = 30 * time.Second
	agentMessageRateLimit    = 8
	agentMessageQueueCap     = 50
)

func agentMessageGuardVerdict(counts inbox.PeerGuardCounts) string {
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

	targetRef := msg.TargetSessionID
	var address inbox.Address
	if seedID := strings.TrimSpace(protocol.Deref(msg.TargetSeedID)); seedID != "" {
		if strings.TrimSpace(targetRef) != "" {
			d.replyAgentMsgError(conn, "ambiguous_target", "a message goes to one place; name a session or a seed, not both")
			return
		}
		if err := d.requireHome(garden.Surface); err != nil {
			d.sendError(conn, err.Error())
			return
		}
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			d.replyAgentMsgError(conn, "seed_not_found", err.Error())
			return
		}
		address = inbox.ToSeed(seed.ID)
	} else {
		member, found, memberErr := d.resolveCrewMember(targetRef)
		if found {
			address = inbox.ToMember(member.ID)
		} else {
			target, code := d.resolveSessionByIDOrPrefix(targetRef)
			if target == nil {
				if memberErr != nil {
					d.sendError(conn, memberErr.Error())
					return
				}
				if code == "session_not_found" {
					d.replyAgentMsgError(conn, "session_or_crew_member_not_found", fmt.Sprintf("no session or crew member matches %q; `attn agent list` names sessions and `attn crew list` names members", strings.TrimSpace(targetRef)))
				} else {
					d.sendError(conn, code)
				}
				return
			}
			address = d.inboxAddressOf(target.ID)
		}
	}
	target := d.inboxHolder(address)
	if target != nil {
		result.TargetSessionID = target.ID
		if sender.ID == target.ID {
			result.Detail = "that is this session; a message to yourself is not a conversation"
			d.replyAgentMsg(conn, result)
			return
		}
	}
	now := time.Now()
	counts, err := d.store.PeerMessageGuardCounts(sender.ID, address, content, now.Add(-agentMessageDedupeWindow), now.Add(-agentMessageRateWindow))
	if err != nil {
		d.logf("agent msg guard counts: %v", err)
		d.sendError(conn, "internal_error")
		return
	}
	if verdict := agentMessageGuardVerdict(counts); verdict != "" {
		result.Detail = verdict
		d.replyAgentMsg(conn, result)
		return
	}
	message := inbox.Message{ID: uuid.NewString(), SenderSessionID: sender.ID, Body: content, CreatedAt: now.UTC().Format(time.RFC3339Nano)}
	if err := d.store.PutPeerMessage(message, address); err != nil {
		d.sendError(conn, "internal_error")
		return
	}
	receipt := d.deliverSavedInbox(address, message.ID, now)
	result.MessageID = receipt.ItemID
	result.Status = protocol.AgentMsgStatusQueued
	if receipt.Rang {
		result.Status = protocol.AgentMsgStatusNotified
	}
	result.Detail = receipt.Detail
	if holder := d.inboxHolder(address); holder != nil {
		result.TargetSessionID = holder.ID
	}
	if receipt.Rang && address.MemberID() != "" {
		result.Detail = "notified " + crew.DisplayName(address.MemberID())
	}

	d.replyAgentMsg(conn, result)
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
	if errors.Is(err, errInboxNoPromptReader) {
		return "queued (target is a shell pane, so attn will not type at it; the message waits for `attn agent inbox` there)"
	}
	if errors.Is(err, errInboxDoorbellOutstanding) {
		return "queued (target already has an inbox doorbell; this message is in the same unread batch)"
	}
	if errors.Is(err, errInboxDoorbellInFlight) {
		return "queued (target's inbox doorbell is already being placed)"
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
	return "queued (target is not taking input right now — lands when it is running again; don't wait for a reply)"
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
