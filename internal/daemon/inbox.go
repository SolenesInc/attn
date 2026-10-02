package daemon

import (
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/inbox"
)

// sendToInbox is the way to put something in front of an agent.
func (d *Daemon) sendToInbox(item inbox.Item) (inbox.Receipt, error) {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	id, err := d.store.PutInbox(item, time.Now())
	if err != nil {
		return inbox.Receipt{}, err
	}
	return d.deliverSavedInbox(item.To, id), nil
}
func (d *Daemon) deliverSavedInbox(to inbox.Address, id string) inbox.Receipt {
	receipt, err := d.deliverInbox(to)
	receipt.ItemID = id
	if err != nil {
		d.logf("inbox: item %s saved for %s; delivery deferred: %v", id, to, err)
		receipt.Detail = "queued (item saved; delivery deferred)"
	}
	return receipt
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
func (d *Daemon) inboxAddressOf(sessionID string) inbox.Address {
	if member := d.crewMemberBoundTo(sessionID); member != "" {
		return inbox.ToMember(member)
	}
	if d.isChiefOfStaffSession(sessionID) {
		return inbox.ToChief()
	}
	return inbox.ToSession(sessionID)
}
func (d *Daemon) inboxRoleAddresses(sessionID string) []inbox.Address {
	addresses := []inbox.Address{inbox.ToSession(sessionID)}
	if member := d.crewMemberBoundTo(sessionID); member != "" {
		addresses = append(addresses, inbox.ToMember(member))
	}
	if d.isChiefOfStaffSession(sessionID) {
		addresses = append(addresses, inbox.ToChief())
	}
	return addresses
}

func (d *Daemon) inboxAddressesOf(sessionID string) ([]inbox.Address, error) {
	addresses := []inbox.Address{inbox.ToSession(sessionID)}
	if d.isChiefOfStaffSession(sessionID) {
		addresses = append(addresses, inbox.ToChief())
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
			member = candidate.ID
			addresses = append(addresses, inbox.ToMember(member))
			break
		}
	}
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{Namespace: garden.Namespace, Collection: garden.CollectionSeeds,
			Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: garden.StatusGrowing}}, Limit: docstore.MaxLimit, After: after})
		if err != nil {
			return nil, err
		}
		for _, doc := range read.Documents {
			seed, err := garden.Decode(doc.Body)
			if err != nil {
				return nil, err
			}
			tender := seed.Tender()
			if tender.Session == sessionID || (member != "" && strings.EqualFold(tender.Member, member)) {
				addresses = append(addresses, inbox.ToSeed(seed.ID))
			}
		}
		if len(read.Documents) < docstore.MaxLimit {
			return addresses, nil
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}
func (d *Daemon) kickSessionInboxAddresses(sessionID string) {
	addresses, err := d.inboxAddressesOf(sessionID)
	if err != nil {
		d.logf("inbox: addresses of %s: %v", sessionID, err)
		return
	}
	d.kickInboxAfterCommit(addresses...)
}
