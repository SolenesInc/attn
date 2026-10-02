package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) handleAgentInbox(conn net.Conn, msg *protocol.AgentInboxMessage) {
	recipient, errCode := d.resolveSessionByIDOrPrefix(msg.RecipientSessionID)
	if recipient == nil {
		d.sendError(conn, "recipient_"+errCode)
		return
	}
	if strings.TrimSpace(protocol.Deref(msg.MessageID)) == "" {
		d.handleAgentInboxBatch(conn, recipient.ID, protocol.Deref(msg.Limit))
		return
	}
	record, readNow, err := d.store.ReadPeerMessage(
		strings.TrimSpace(protocol.Deref(msg.MessageID)), recipient.ID, d.inboxAddressesOf(recipient.ID), time.Now(),
	)
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true, AgentInboxResult: d.peerMessageResult(record),
	})
	if readNow {
		d.sentToInbox(record.To)
	}
}

func (d *Daemon) handleAgentInboxBatch(conn net.Conn, recipientSessionID string, limit int) {
	d.lockGardenRoles()
	err := d.discardUncoveredSeedBells(recipientSessionID)
	var deliveries []inbox.Delivery
	var remaining int
	if err == nil {
		deliveries, remaining, err = d.store.ReadInbox(d.inboxAddressesOf(recipientSessionID), recipientSessionID, limit, time.Now())
	}
	d.unlockGardenRoles()
	if err != nil {
		d.logf("agent inbox batch: session=%s err=%v", recipientSessionID, err)
		d.replyAgentMsgError(conn, "internal_error", "the agent inbox could not be read")
		return
	}
	items := make([]protocol.AgentInboxItem, 0, len(deliveries))
	d.noteCrewRestartMailboxRead(deliveries)
	for _, delivery := range deliveries {
		item := protocol.AgentInboxItem{
			Address: delivery.Item.To.String(), ItemID: delivery.Item.ID, Kind: string(delivery.Item.Kind),
			Content: mailboxItemContent(delivery), CreatedAt: delivery.Item.CreatedAt,
			NotifiedAt: delivery.Item.NotifiedAt, ReadAt: delivery.Item.ReadAt,
		}
		if delivery.Item.Source != "" {
			item.SourceID = protocol.Ptr(delivery.Item.Source)
		}
		if delivery.Item.Hint != "" {
			item.Hint = protocol.Ptr(delivery.Item.Hint)
		}
		if delivery.Peer != nil {
			item.SenderSessionID = protocol.Ptr(delivery.Peer.SenderSessionID)
			label := shortSessionID(delivery.Peer.SenderSessionID)
			if sender := d.store.Get(delivery.Peer.SenderSessionID); sender != nil {
				label = d.sessionOriginName(sender)
			}
			item.SenderLabel = protocol.Ptr(label)
		}
		items = append(items, item)
	}
	for _, delivery := range deliveries {
		d.sentToInbox(delivery.Item.To)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		AgentInboxBatchResult: &protocol.AgentInboxBatchResult{
			Items: items, Remaining: remaining,
		},
	})
}

func mailboxItemContent(delivery inbox.Delivery) string {
	switch delivery.Item.Kind {
	case inbox.SeedUpdate:
		return prompts.RenderText("session", "garden-update", prompts.Values{"seed_id": delivery.Item.Source, "event_kind": delivery.Item.Hint})
	case inbox.PeerMessage:
		if delivery.Peer != nil {
			return delivery.Peer.Body
		}
	case inbox.Notice:
		return delivery.Item.Text
	}
	if delivery.Item.Text != "" {
		return delivery.Item.Text
	}
	return delivery.Item.Hint
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

func (d *Daemon) peerMessageResult(record inbox.PeerRecord) *protocol.AgentPeerMessage {
	senderLabel := shortSessionID(record.Message.SenderSessionID)
	if sender := d.store.Get(record.Message.SenderSessionID); sender != nil {
		senderLabel = d.sessionOriginName(sender)
	}
	target := record.ReadBy
	if target == "" {
		target = record.To.SessionID()
		if holder := d.inboxHolder(record.To); holder != nil {
			target = holder.ID
		}
	}
	result := &protocol.AgentPeerMessage{
		MessageID: record.Message.ID, SenderSessionID: record.Message.SenderSessionID,
		SenderLabel: senderLabel, TargetSessionID: target,
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
