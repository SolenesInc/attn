package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
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
	sender, _ := r.Party()
	asking, _ := r.AskingSession()
	address, err := d.resolveAddress(r, b, msg.To)
	if err != nil {
		d.replyTargetError(conn, err)
		return
	}
	content := strings.TrimSpace(msg.Content)
	result := &protocol.AgentMsgResult{Status: protocol.AgentMsgStatusRefused, To: address.Ref(), ToName: d.addressName(address, b)}
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

	target, err := (delivery{d, b}).recipientOf(address)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if target.ring != nil {
		result.TargetSessionID = protocol.Ptr(target.ring.ID)
		if asking == target.ring.ID {
			result.Detail = "that is this session; a message to yourself is not a conversation"
			d.replyAgentMsg(conn, result)
			return
		}
	}
	if target.remote != nil {
		d.replyAgentMsgError(conn, "remote_delivery_unsupported", fmt.Sprintf("session %s runs on outpost %s; messaging outpost sessions is unsupported", shortSessionID(target.remote.ID), protocol.Deref(target.remote.EndpointID)))
		return
	}
	now := time.Now()
	counts, err := d.store.PeerMessageGuardCounts(sender, address, content, now.Add(-agentMessageDedupeWindow), now.Add(-agentMessageRateWindow))
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
	message := inbox.Message{ID: uuid.NewString(), Sender: sender, Body: content, CreatedAt: now.UTC().Format(time.RFC3339Nano)}
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
	if receipt.SessionID != "" {
		result.TargetSessionID = protocol.Ptr(receipt.SessionID)
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

func sessionDisplayName(session *protocol.Session) string {
	if label := strings.TrimSpace(session.Label); label != "" {
		return label
	}
	return shortSessionID(session.ID)
}

func shortSessionID(id protocol.SessionID) string {
	if len(id) <= agentShortIDLength {
		return string(id)
	}
	return string(id[:agentShortIDLength])
}
