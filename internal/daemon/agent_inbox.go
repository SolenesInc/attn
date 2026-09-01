package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleAgentInbox(conn net.Conn, msg *protocol.AgentInboxMessage) {
	recipient, errCode := d.resolveSessionByIDOrPrefix(msg.RecipientSessionID)
	if recipient == nil {
		d.sendError(conn, "recipient_"+errCode)
		return
	}
	record, readNow, err := d.store.ReadPeerMessage(
		strings.TrimSpace(msg.MessageID), recipient.ID, time.Now(),
	)
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true, AgentInboxResult: d.peerMessageResult(record),
	})
	if readNow {
		d.noteQueuedAgentMailboxItem(recipient.ID)
		go d.drainQueuedAgentMailboxItems(recipient.ID)
	}
}

func (d *Daemon) handleAgentMsgStatus(conn net.Conn, msg *protocol.AgentMsgStatusMessage) {
	sender, errCode := d.resolveSessionByIDOrPrefix(msg.SenderSessionID)
	if sender == nil {
		d.sendError(conn, "sender_"+errCode)
		return
	}
	record, err := d.store.PeerMessageRecord(strings.TrimSpace(msg.MessageID))
	if err != nil || record.Message.SenderSessionID != sender.ID {
		if err != nil && !errors.Is(err, store.ErrPeerMessageNotFound) {
			d.logf("agent msg status: id=%s err=%v", msg.MessageID, err)
		}
		d.replyAgentMsgError(conn, "message_not_found", "no peer message with that id belongs to this sender")
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true, AgentMsgStatusResult: d.peerMessageResult(record),
	})
}

func (d *Daemon) replyPeerMessageError(conn net.Conn, err error) {
	switch {
	case errors.Is(err, store.ErrPeerMessageNotFound):
		d.replyAgentMsgError(conn, "message_not_found", "no notified peer message with that id belongs to this recipient")
	case errors.Is(err, store.ErrPeerMessageNotNotified):
		d.replyAgentMsgError(conn, "message_not_notified", "that message is still queued and cannot be read before its notification lands")
	default:
		d.logf("agent inbox: %v", err)
		d.replyAgentMsgError(conn, "internal_error", "the peer message could not be read")
	}
}

func (d *Daemon) peerMessageResult(record agentmailbox.PeerRecord) *protocol.AgentPeerMessage {
	senderLabel := shortSessionID(record.Message.SenderSessionID)
	if sender := d.store.Get(record.Message.SenderSessionID); sender != nil {
		senderLabel = d.sessionOriginName(sender)
	}
	result := &protocol.AgentPeerMessage{
		MessageID: record.Message.ID, SenderSessionID: record.Message.SenderSessionID,
		SenderLabel: senderLabel, TargetSessionID: record.RecipientSessionID,
		Content: record.Message.Body, State: protocol.AgentMessageState(record.State()),
		CreatedAt: record.Message.CreatedAt,
	}
	if record.NotifiedAt != "" {
		result.NotifiedAt = protocol.Ptr(record.NotifiedAt)
	}
	if record.ReadAt != "" {
		result.ReadAt = protocol.Ptr(record.ReadAt)
	}
	return result
}
