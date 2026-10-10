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
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) handleAgentInbox(conn net.Conn, msg *protocol.AgentInboxMessage) {
	recipient, errCode := d.resolveSessionByIDOrPrefix(string(msg.RecipientSessionID), "")
	if recipient == nil {
		d.sendError(conn, "recipient_"+errCode)
		return
	}
	if strings.TrimSpace(protocol.Deref(msg.MessageID)) == "" {
		d.handleAgentInboxBatch(conn, recipient.ID, protocol.Deref(msg.Limit))
		return
	}
	b, err := d.bindings()
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	addresses, err := d.mailboxesOf(recipient.ID, b)
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	messageID := strings.TrimSpace(protocol.Deref(msg.MessageID))
	stored, err := d.store.PeerMessageRecord(messageID)
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	if seedAddressID(stored.To) != "" {
		to, err := (delivery{d, b}).recipientOf(stored.To)
		if err != nil {
			d.replyPeerMessageError(conn, err)
			return
		}
		if to.ring != nil && to.ring.ID == recipient.ID {
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
		Ok: true, AgentInboxResult: d.peerMessageResult(record, b),
	})
	if readNow {
		d.kickInboxAfterCommit(record.To)
	}
}

func (d *Daemon) handleAgentInboxBatch(conn net.Conn, recipientSessionID protocol.SessionID, limit int) {
	b, err := d.bindings()
	if err != nil {
		d.replyPeerMessageError(conn, err)
		return
	}
	d.lockGardenRoles()
	addresses, err := d.mailboxesOf(recipientSessionID, b)
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
			Address: delivery.Item.To.Ref(), ItemID: delivery.Item.ID, Kind: string(delivery.Item.Kind),
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
			item.Sender = protocol.Ptr(d.partyView(delivery.Peer.Sender, b))
			item.ReplyTo = protocol.Ptr(d.replyTo(delivery.Peer.Sender))
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
	b, err := d.bindings()
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	r, err := d.requestFromSession(msg.SenderSessionID, b)
	if err != nil {
		d.replyTargetError(conn, err)
		return
	}
	sender, _ := r.Party()
	record, err := d.store.PeerMessageRecord(strings.TrimSpace(msg.MessageID))
	if err != nil || record.Message.Sender != sender {
		if err != nil && !errors.Is(err, store.ErrPeerMessageNotFound) {
			d.logf("agent msg status: id=%s err=%v", msg.MessageID, err)
		}
		d.replyAgentMsgError(conn, "message_not_found", "no peer message with that id belongs to this sender")
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true, AgentMsgStatusResult: d.peerMessageResult(record, b),
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

func (d *Daemon) peerMessageResult(record inbox.PeerRecord, b who.Bindings) *protocol.AgentPeerMessage {
	result := &protocol.AgentPeerMessage{
		MessageID: record.Message.ID, Sender: d.partyView(record.Message.Sender, b), ReplyTo: d.replyTo(record.Message.Sender),
		To: record.To.Ref(), ToName: d.addressName(record.To, b), Content: record.Message.Body, State: protocol.AgentMessageState(record.State()), CreatedAt: record.Message.CreatedAt,
	}
	if record.ReadBy != "" {
		result.ReadBy = protocol.Ptr(record.ReadBy)
	}
	if record.NotifiedAt != "" {
		result.NotifiedAt = protocol.Ptr(record.NotifiedAt)
	}
	if record.ReadAt != "" {
		result.ReadAt = protocol.Ptr(record.ReadAt)
	}
	return result
}
