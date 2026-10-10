package daemon

import (
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

// sendToInbox is the way to put something in front of an agent.
func (d *Daemon) sendToInbox(item inbox.Item) (inbox.Receipt, error) {
	if err := d.mailRefusal(item.To); err != nil {
		return inbox.Receipt{}, err
	}
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	savedAt := time.Now()
	id, err := d.store.PutInbox(item, savedAt)
	if err != nil {
		return inbox.Receipt{}, err
	}
	return d.deliverSavedInbox(item.To, id, savedAt), nil
}
func (d *Daemon) deliverSavedInbox(to who.Address, id string, savedAt time.Time) inbox.Receipt {
	receipt, err := d.deliverInbox(to)
	receipt.ItemID = id
	if err != nil {
		d.logf("inbox: item %s saved for %s; delivery deferred: %v", id, to, err)
		receipt.Detail = "queued (delivery deferred: " + err.Error() + ")"
		return receipt
	}
	// A concurrent delivery, such as the holder's prompt-ready kick, may place the ring that covers this item.
	if !receipt.Rang && d.inboxItemRungSince(id, savedAt) {
		receipt.Rang, receipt.Outstanding, receipt.Detail = true, false, "notified"
		if holder := d.inboxHolder(to); holder != nil {
			receipt.SessionID = holder.ID
			receipt.Detail = "notified " + sessionDisplayName(holder)
		}
	}
	return receipt
}
func (d *Daemon) inboxItemRungSince(id string, since time.Time) bool {
	item, found, err := d.store.InboxItem(id)
	if err != nil || !found {
		return false
	}
	notified, err := time.Parse(time.RFC3339Nano, item.NotifiedAt)
	return err == nil && !notified.Before(since)
}
func (d *Daemon) kickInboxAfterCommit(addresses ...who.Address) {
	for _, a := range addresses {
		d.kickInbox(a)
	}
}
func (d *Daemon) withdrawFromInbox(to who.Address, kind inbox.Kind, key string) error {
	if err := d.store.WithdrawInbox(to, kind, key); err != nil {
		return err
	}
	d.kickInbox(to)
	return nil
}

func (d *Daemon) kickSessionInboxAddresses(sessionID protocol.SessionID) {
	d.life.Go("inbox-session-ready", func() {
		b, err := d.bindings()
		if err != nil {
			d.logf("inbox bindings: %v", err)
			return
		}
		addresses, err := d.mailboxesOf(sessionID, b)
		if err != nil {
			d.logf("inbox: addresses of %s: %v", sessionID, err)
			return
		}
		d.kickInboxAfterCommit(addresses...)
	})
}
