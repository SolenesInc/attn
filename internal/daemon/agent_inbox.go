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
		d.handleAgentInboxBatch(conn, recipient.ID, recipient.ProfileID, protocol.Deref(msg.Limit))
		return
	}
	addresses, err := d.inboxAddressesOf(recipient.ID)
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	messageID := strings.TrimSpace(protocol.Deref(msg.MessageID))
	storedItem, found, err := d.store.InboxItem(store.QuickCaptureInboxID(recipient.ProfileID, messageID))
	if found {
		messageID = storedItem.ID
	}
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	if found && storedItem.Kind == inbox.QuickCapture {
		item, err := d.store.ReadInboxItem(messageID, recipient.ID, addresses, time.Now())
		if err != nil {
			d.replyAgentMsgError(conn, "message_not_found", err.Error())
			return
		}
		quickCapture, err := d.store.QuickCapture(recipient.ProfileID, item.Source)
		if err != nil {
			d.replyPeerMessageError(conn, err)
			return
		}
		result := &protocol.AgentInboxItem{Address: item.To.String(), ItemID: item.Source, Kind: string(item.Kind), SourceID: protocol.Ptr(item.Source), Content: item.Text, CreatedAt: item.CreatedAt, NotifiedAt: item.NotifiedAt, ReadAt: item.ReadAt, Attachments: quickCapture.Attachments}
		_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, AgentInboxItemResult: result})
		d.publishQuickCaptureRead(recipient.ProfileID, quickCapture.ID, item.ReadAt)
		d.kickInboxAfterCommit(item.To)
		return
	}
	stored, err := d.store.PeerMessageRecord(messageID)
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	if stored.To.SeedID() != "" {
		holder, _, err := d.inboxRecipient(stored.To)
		if err != nil {
			d.replyPeerMessageError(conn, err)
			return
		}
		if holder != nil && holder.ID == recipient.ID {
			addresses = append(addresses, stored.To)
		}
	}
	record, readNow, err := d.store.ReadPeerMessage(
		messageID, recipient.ID, addresses, time.Now(),
	)
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true, AgentInboxResult: d.peerMessageResult(record),
	})
	if readNow {
		d.kickInboxAfterCommit(record.To)
	}
}

func (d *Daemon) handleAgentInboxBatch(conn net.Conn, recipientSessionID, profileID string, limit int) {
	d.lockGardenRoles()
	addresses, err := d.inboxAddressesOf(recipientSessionID)
	for _, address := range addresses {
		if err = d.discardIneligibleGardenSeedBellsLocked(address); err != nil {
			break
		}
	}
	var deliveries []store.InboxDelivery
	var remaining int
	if err == nil {
		deliveries, remaining, err = d.store.ReadInbox(addresses, recipientSessionID, limit, time.Now())
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
		if delivery.Item.Kind == inbox.QuickCapture {
			item.ItemID = delivery.Item.Source
			record, err := d.store.QuickCapture(profileID, delivery.Item.Source)
			if err != nil {
				d.logf("inbox quick capture assets: %v", err)
			} else {
				item.Attachments = record.Attachments
			}
			d.publishQuickCaptureRead(profileID, delivery.Item.Source, delivery.Item.ReadAt)
		}
		if delivery.Peer != nil {
			item.SenderSessionID = protocol.Ptr(delivery.Peer.SenderSessionID)
			label := shortSessionID(delivery.Peer.SenderSessionID)
			if sender := d.store.Get(delivery.Peer.SenderSessionID); sender != nil {
				label = sessionDisplayName(sender)
			}
			item.SenderLabel = protocol.Ptr(label)
		}
		items = append(items, item)
	}
	d.kickInboxAfterCommit(addresses...)
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		AgentInboxBatchResult: &protocol.AgentInboxBatchResult{
			Items: items, Remaining: remaining,
		},
	})
}

func mailboxItemContent(delivery store.InboxDelivery) string {
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
		senderLabel = sessionDisplayName(sender)
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
