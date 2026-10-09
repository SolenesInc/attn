package daemon

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

// sendToInbox is the way to put something in front of an agent.
func (d *Daemon) sendToInbox(item inbox.Item) (inbox.Receipt, error) {
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
func (d *Daemon) deliverSavedInbox(to inbox.Address, id string, savedAt time.Time) inbox.Receipt {
	receipt, err := d.deliverInbox(to)
	receipt.ItemID = id
	if err != nil {
		d.logf("inbox: item %s saved for %s; delivery deferred: %v", id, to, err)
		receipt.Detail = "queued (item saved; delivery deferred)"
		return receipt
	}
	// A concurrent delivery, such as the holder's prompt-ready kick, may place the ring that covers this item.
	if !receipt.Rang && d.inboxItemRungSince(id, savedAt) {
		receipt.Rang, receipt.Outstanding, receipt.Detail = true, false, "notified"
		if holder := d.inboxHolder(to); holder != nil {
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
func (d *Daemon) kickInboxAfterCommit(addresses ...inbox.Address) {
	for _, a := range addresses {
		d.kickInbox(a)
	}
}
func (d *Daemon) withdrawFromInbox(to inbox.Address, kind inbox.Kind, key string) error {
	if err := d.store.WithdrawInbox(to, kind, key); err != nil {
		return err
	}
	d.kickInbox(to)
	return nil
}

// chiefAddressOf is the chief inbox of the profile this session is chief of.
func (d *Daemon) chiefAddressOf(sessionID protocol.SessionID) (inbox.Address, bool) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" || d.store == nil {
		return inbox.Address{}, false
	}
	profileID, err := d.store.SessionProfileID(sessionID)
	if err != nil || profileID == "" || d.chiefOfProfile(profileID) != sessionID {
		return inbox.Address{}, false
	}
	return inbox.ToChief(profileID), true
}
func (d *Daemon) inboxAddressOf(sessionID protocol.SessionID) inbox.Address {
	if member := d.crewMemberBoundTo(sessionID); member != "" {
		return inbox.ToMember(member)
	}
	if chief, ok := d.chiefAddressOf(sessionID); ok {
		return chief
	}
	return inbox.ToSession(sessionID)
}
func (d *Daemon) inboxRoleAddresses(sessionID protocol.SessionID) []inbox.Address {
	addresses := []inbox.Address{inbox.ToSession(sessionID)}
	if member := d.crewMemberBoundTo(sessionID); member != "" {
		addresses = append(addresses, inbox.ToMember(member))
	}
	if chief, ok := d.chiefAddressOf(sessionID); ok {
		addresses = append(addresses, chief)
	}
	return addresses
}

func (d *Daemon) inboxAddressesOf(sessionID protocol.SessionID) ([]inbox.Address, error) {
	addresses := []inbox.Address{inbox.ToSession(sessionID)}
	if chief, ok := d.chiefAddressOf(sessionID); ok {
		addresses = append(addresses, chief)
	}
	status, err := d.enrollmentStatus()
	if err != nil {
		return nil, err
	}
	if !status.IsHome() {
		return addresses, nil
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		return nil, err
	}
	member := ""
	for _, candidate := range members {
		if candidate.BindingSession == sessionID && d.crewBindingLive(candidate) {
			member = candidate.Key.String()
			addresses = append(addresses, inbox.ToMember(member))
			break
		}
	}
	pending, err := d.store.PendingSeedInboxAddresses()
	if err != nil {
		return nil, err
	}
	for _, address := range pending {
		seed, _, err := d.readSeed(address.SeedID())
		if err != nil {
			return nil, err
		}
		tender := seed.Tender()
		if tender.Session == sessionID || (member != "" && strings.EqualFold(tender.Member, member) && d.crewProfileID(member) == seed.ProfileID) {
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}
func (d *Daemon) kickSessionInboxAddresses(sessionID protocol.SessionID) {
	d.life.Go("inbox-session-ready", func() {
		addresses, err := d.inboxAddressesOf(sessionID)
		if err != nil {
			d.logf("inbox: addresses of %s: %v", sessionID, err)
			return
		}
		d.kickInboxAfterCommit(addresses...)
	})
}
