package daemon

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
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
func (d *Daemon) deliverSavedInbox(to who.Address, id string, savedAt time.Time) inbox.Receipt {
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
func (d *Daemon) kickInboxAfterCommit(addresses ...who.Address) {
	for _, a := range addresses {
		d.kickInbox(a)
	}
}
func memberAddress(key string) (who.Address, error) {
	k, err := who.ParseMemberKey(key)
	if err != nil {
		return who.Address{}, err
	}
	return who.Member(k).Address(), nil
}

func (d *Daemon) partyOfSession(id protocol.SessionID) who.Party {
	if member, bound := d.crewMemberBoundToSession(id); bound {
		return who.Member(member.Key)
	}
	return who.Session(id)
}

func (d *Daemon) partyView(p who.Party) protocol.PartyView {
	view := protocol.PartyView{Ref: protocol.PartyRef(p.String())}
	if key, member := p.Member(); member {
		view.Name = d.crewMemberName(key.String())
		return view
	}
	id, _ := p.Session()
	view.Name = shortSessionID(id)
	if session := d.store.Get(id); session != nil {
		view.Name = sessionDisplayName(session)
	}
	return view
}

func (d *Daemon) replyTo(p who.Party) string {
	if key, member := p.Member(); member {
		return d.crewMemberName(key.String())
	}
	id, _ := p.Session()
	return shortSessionID(id)
}

func (d *Daemon) withdrawFromInbox(to who.Address, kind inbox.Kind, key string) error {
	if err := d.store.WithdrawInbox(to, kind, key); err != nil {
		return err
	}
	d.kickInbox(to)
	return nil
}

// chiefAddressOf is the chief inbox of the profile this session is chief of.
func (d *Daemon) chiefAddressOf(sessionID protocol.SessionID) (who.Address, bool) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" || d.store == nil {
		return who.Address{}, false
	}
	profileID, err := d.store.SessionProfileID(sessionID)
	if err != nil || profileID == "" || d.chiefOfProfile(profileID) != sessionID {
		return who.Address{}, false
	}
	return who.ToChief(profileID), true
}
func (d *Daemon) inboxAddressOf(sessionID protocol.SessionID) who.Address {
	if member, bound := d.crewMemberBoundToSession(sessionID); bound {
		return who.Member(member.Key).Address()
	}
	if chief, ok := d.chiefAddressOf(sessionID); ok {
		return chief
	}
	return who.ToSession(sessionID)
}
func (d *Daemon) inboxRoleAddresses(sessionID protocol.SessionID) []who.Address {
	addresses := []who.Address{who.ToSession(sessionID)}
	if member, bound := d.crewMemberBoundToSession(sessionID); bound {
		addresses = append(addresses, who.Member(member.Key).Address())
	}
	if chief, ok := d.chiefAddressOf(sessionID); ok {
		addresses = append(addresses, chief)
	}
	return addresses
}

func (d *Daemon) inboxAddressesOf(sessionID protocol.SessionID) ([]who.Address, error) {
	addresses := []who.Address{who.ToSession(sessionID)}
	if chief, ok := d.chiefAddressOf(sessionID); ok {
		addresses = append(addresses, chief)
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		return nil, err
	}
	member := ""
	for _, candidate := range members {
		if candidate.BindingSession == sessionID && d.crewBindingLive(candidate) {
			member = candidate.ID
			addresses = append(addresses, who.Member(candidate.Key).Address())
			break
		}
	}
	pending, err := d.store.PendingSeedInboxAddresses()
	if err != nil {
		return nil, err
	}
	for _, address := range pending {
		seedID, _ := address.TenderOf()
		seed, _, err := d.readSeed(seedID)
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
