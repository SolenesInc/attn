package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) runAgentInbox(ctx context.Context, c command.Caller, a command.AgentInboxArgs) (any, error) {
	messageID := strings.TrimSpace(a.Message)
	limit := inbox.DefaultInboxLimit
	if a.Limit != nil {
		if messageID != "" {
			return nil, command.Usage("{limit} cannot be used with a message id")
		}
		limit = int(*a.Limit)
	}
	if limit < 1 || limit > inbox.MaxInboxLimit {
		return nil, command.Usage("{limit} must be between 1 and %d", inbox.MaxInboxLimit)
	}
	recipient, err := d.mailboxSession(c)
	if err != nil {
		return nil, err
	}
	var read inboxRead
	if messageID == "" {
		read, err = d.agentInboxBatch(recipient.ID, limit)
	} else {
		read, err = d.agentInboxEntry(recipient, messageID)
	}
	if err != nil {
		return nil, err
	}
	if a.Render {
		read.result.Text = protocol.Ptr(agentInboxText(read.result))
	}
	return command.Then{Result: read.result, After: read.after, ShownOnce: read.markedRead}, nil
}

type inboxRead struct {
	result     *protocol.AgentInboxBatchResult
	after      func()
	markedRead bool
}

func (d *Daemon) mailboxSession(c command.Caller) (*protocol.Session, error) {
	if c.Session == "" {
		return nil, command.Refuse(command.CodeNoSession, "no session identity: run this inside an attn session or pass {session} <id>")
	}
	session := d.store.Get(c.Session)
	if session == nil {
		return nil, command.Refuse(command.CodeSessionNotFound, "the session %q is not on this daemon", c.Session)
	}
	return session, nil
}

func peerMessageRefusal(d *Daemon, messageID string, err error) error {
	switch {
	case errors.Is(err, store.ErrPeerMessageNotFound):
		return command.Refuse("message_not_found", "message %q was not found for this session", messageID)
	case errors.Is(err, store.ErrPeerMessageNotNotified):
		return command.Refuse("message_not_notified", "message %q is still queued; read it after its notification lands", messageID)
	default:
		d.logf("agent inbox: %v", err)
		return errors.New("the peer message could not be read")
	}
}

func (d *Daemon) agentInboxEntry(recipient *protocol.Session, messageID string) (inboxRead, error) {
	addresses, err := d.inboxAddressesOf(recipient.ID)
	if err != nil {
		return inboxRead{}, peerMessageRefusal(d, messageID, err)
	}
	asked := messageID
	storedItem, found, err := d.store.InboxItem(store.QuickCaptureInboxID(recipient.ProfileID, messageID))
	if found {
		messageID = storedItem.ID
	}
	if err != nil {
		return inboxRead{}, peerMessageRefusal(d, asked, err)
	}
	result := &protocol.AgentInboxBatchResult{Items: []protocol.AgentInboxItem{}}
	if found && storedItem.Kind == inbox.QuickCapture {
		item, readNow, err := d.store.ReadInboxItem(messageID, string(recipient.ID), addresses, time.Now())
		if err != nil {
			return inboxRead{}, command.Refuse("message_not_found", "message %q was not found for this session", asked)
		}
		quickCapture, err := d.store.QuickCapture(recipient.ProfileID, item.Source)
		if err != nil {
			return inboxRead{}, peerMessageRefusal(d, asked, err)
		}
		result.Item = &protocol.AgentInboxItem{Address: item.To.String(), ItemID: item.Source, Kind: string(item.Kind), SourceID: protocol.Ptr(item.Source), Content: item.Text, CreatedAt: item.CreatedAt, NotifiedAt: item.NotifiedAt, ReadAt: item.ReadAt, Attachments: quickCapture.Attachments}
		return inboxRead{result: result, markedRead: readNow, after: func() {
			if readNow {
				d.publishQuickCaptureRead(recipient.ProfileID, quickCapture.ID, item.ReadAt)
				d.kickInboxAfterCommit(item.To)
			}
		}}, nil
	}
	stored, err := d.store.PeerMessageRecord(messageID)
	if err != nil {
		return inboxRead{}, peerMessageRefusal(d, asked, err)
	}
	if _, seed := stored.To.TenderOf(); seed {
		holder, _, err := d.inboxRecipient(stored.To)
		if err != nil {
			return inboxRead{}, peerMessageRefusal(d, asked, err)
		}
		if holder != nil && holder.ID == recipient.ID {
			addresses = append(addresses, stored.To)
		}
	}
	record, readNow, err := d.store.ReadPeerMessage(messageID, recipient.ID, addresses, time.Now())
	if err != nil {
		return inboxRead{}, peerMessageRefusal(d, asked, err)
	}
	result.Message = d.peerMessageResult(record)
	if !readNow {
		return inboxRead{result: result}, nil
	}
	d.endStandbyOnceMailRead(record.To)
	return inboxRead{result: result, markedRead: true, after: func() { d.kickInboxAfterCommit(record.To) }}, nil
}

func (d *Daemon) agentInboxBatch(recipientSessionID protocol.SessionID, limit int) (inboxRead, error) {
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
		return inboxRead{}, errors.New("the agent inbox could not be read")
	}
	items := make([]protocol.AgentInboxItem, 0, len(deliveries))
	parties := d.partiesNow()
	tools := d.sessionCarriesAgentTools(recipientSessionID)
	var read []who.Address
	for _, delivery := range deliveries {
		if !slices.Contains(read, delivery.Item.To) {
			read = append(read, delivery.Item.To)
		}
		item := protocol.AgentInboxItem{
			Address: delivery.Item.To.String(), ItemID: delivery.Item.ID, Kind: string(delivery.Item.Kind),
			Content: mailboxItemContent(delivery, tools), CreatedAt: delivery.Item.CreatedAt,
			NotifiedAt: delivery.Item.NotifiedAt, ReadAt: delivery.Item.ReadAt,
		}
		if delivery.Item.Source != "" {
			item.SourceID = protocol.Ptr(delivery.Item.Source)
		}
		if delivery.Item.Hint != "" {
			item.Hint = protocol.Ptr(delivery.Item.Hint)
		}
		if delivery.Peer != nil {
			item.Sender = protocol.Ptr(parties.view(delivery.Peer.Sender))
			item.ReplyTo = protocol.Ptr(d.replyTo(delivery.Peer.Sender))
		}
		items = append(items, item)
	}
	d.endStandbyOnceMailRead(read...)
	after := func() { d.kickInboxAfterCommit(addresses...) }
	result := &protocol.AgentInboxBatchResult{Items: items, Remaining: remaining}
	if len(items) > 0 && d.inNotificationRun(recipientSessionID) {
		switch {
		case d.quietNotifications():
			result.QuietNotifications = protocol.Ptr(true)
		case d.on(setting.PhoneAccessEnabled):
			result.PhoneStaysSilent = protocol.Ptr(true)
		}
	}
	return inboxRead{result: result, after: after, markedRead: len(items) > 0}, nil
}

func mailboxItemContent(delivery store.InboxDelivery, tools bool) string {
	switch delivery.Item.Kind {
	case inbox.SeedUpdate:
		change := delivery.Item.Text
		if change == "" {
			change = delivery.Item.Hint
		}
		after := ""
		if delivery.Item.AfterSeq > 0 {
			after = strconv.FormatInt(delivery.Item.AfterSeq, 10)
		}
		return prompts.RenderText("session", "garden-update", prompts.Values{"seed_id": delivery.Item.Source, "change": change, "after": after, "agent_tools": strconv.FormatBool(tools)})
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

func (d *Daemon) runAgentMsgStatus(ctx context.Context, c command.Caller, a command.AgentMsgStatusArgs) (any, error) {
	sender, err := d.mailboxSession(c)
	if err != nil {
		return nil, err
	}
	messageID := strings.TrimSpace(a.Message)
	record, err := d.store.PeerMessageRecord(messageID)
	if err != nil || record.Message.Sender != d.partyOfSession(sender.ID) {
		if err != nil && !errors.Is(err, store.ErrPeerMessageNotFound) {
			d.logf("agent msg status: id=%s err=%v", messageID, err)
		}
		return nil, command.Refuse("message_not_found", "message %q was not found for this session", messageID)
	}
	return d.peerMessageResult(record), nil
}

func (d *Daemon) peerMessageResult(record inbox.PeerRecord) *protocol.AgentPeerMessage {
	target := record.ReadBy
	if target == "" {
		target, _ = record.To.Session()
		if holder := d.inboxHolder(record.To); holder != nil {
			target = holder.ID
		}
	}
	result := &protocol.AgentPeerMessage{
		MessageID: record.Message.ID, Sender: d.partyView(record.Message.Sender),
		ReplyTo: d.replyTo(record.Message.Sender), TargetSessionID: string(target),
		Content: record.Message.Body, State: protocol.AgentMessageState(record.State()),
		CreatedAt: record.Message.CreatedAt,
	}
	if record.Message.Turn != "" {
		result.Turn = protocol.Ptr(record.Message.Turn)
	}
	if record.DeliveredAs != "" {
		result.DeliveredAs = protocol.Ptr(record.DeliveredAs)
	}
	if record.Message.AsUser {
		result.AsUser = protocol.Ptr(true)
	}
	if record.NotifiedAt != "" {
		result.NotifiedAt = protocol.Ptr(record.NotifiedAt)
	}
	if record.State() == inbox.StateRead {
		result.ReadAt = protocol.Ptr(record.ReadAt)
	}
	return result
}

func agentInboxText(result *protocol.AgentInboxBatchResult) string {
	switch {
	case result.Message != nil:
		return agentPeerMessageText(result.Message.Sender, result.Message.ReplyTo, result.Message.Content)
	case result.Item != nil:
		return agentInboxText(&protocol.AgentInboxBatchResult{Items: []protocol.AgentInboxItem{*result.Item}})
	}
	if len(result.Items) == 0 {
		return prompts.RenderText("session", "inbox-empty", nil)
	}
	var lines []string
	for _, item := range result.Items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			content = item.Kind
			if sourceID := strings.TrimSpace(protocol.Deref(item.SourceID)); sourceID != "" {
				content += " " + sourceID
			}
		}
		switch item.Kind {
		case string(inbox.QuickCapture):
			lines = append(lines, inbox.QuickCaptureText(item.Content, item.Attachments))
		case string(inbox.Notice):
			lines = append(lines, content)
		case string(inbox.PeerMessage):
			lines = append(lines, agentPeerMessageText(protocol.Deref(item.Sender), protocol.Deref(item.ReplyTo), content))
		default:
			lines = append(lines, prompts.RenderText("session", "inbox-item", prompts.Values{"content": content}))
		}
	}
	if result.Remaining > 0 {
		lines = append(lines, prompts.RenderText("session", "inbox-more", prompts.Values{"remaining": strconv.Itoa(result.Remaining)}))
	}
	if protocol.Deref(result.QuietNotifications) {
		lines = append(lines, prompts.RenderText("session", "inbox-quiet", nil))
	}
	if protocol.Deref(result.PhoneStaysSilent) {
		lines = append(lines, prompts.RenderText("session", "inbox-phone-silent", nil))
	}
	return strings.Join(lines, "\n")
}

func agentPeerMessageText(sender protocol.PartyView, replyTo, content string) string {
	return peerMessageText("peer-message", sender, replyTo, content)
}

func (d *Daemon) peerRingText(message inbox.Message, tools bool) string {
	event := "peer-message-steer"
	if message.Turn == protocol.PeerTurnInterrupt {
		event = "peer-message-interrupt"
	}
	values := peerMessageValues(d.partyView(message.Sender), d.replyTo(message.Sender), message.Body)
	values["agent_tools"] = strconv.FormatBool(tools)
	return prompts.RenderText("session", event, values)
}

func peerMessageText(event string, sender protocol.PartyView, replyTo, content string) string {
	return prompts.RenderText("session", event, peerMessageValues(sender, replyTo, content))
}

func peerMessageValues(sender protocol.PartyView, replyTo, content string) prompts.Values {
	origin := replyTo
	if name := strings.TrimSpace(sender.Name); name != "" && name != origin {
		origin = fmt.Sprintf("%s (%s)", origin, name)
	}
	return prompts.Values{"origin": origin, "message": content, "reply_to": replyTo}
}
