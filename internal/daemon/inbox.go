package daemon

import (
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/inbox"
)

func (d *Daemon) sendToInbox(item inbox.Item) (inbox.Receipt, error) {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	id, err := d.store.PutInbox(item, time.Now())
	if err != nil {
		return inbox.Receipt{}, err
	}
	receipt, err := d.deliverInbox(item.To)
	receipt.ItemID = id
	if memberID := item.To.MemberID(); memberID != "" && d.inboxHolder(item.To) == nil {
		if member, _, lookupErr := d.crewMember(memberID); lookupErr == nil {
			ledger := d.crewWakeLedger()
			ledger.Stamps = parseWakeStamps(member.AutonomousWakes)
			if _, refusal := ledger.Allows(memberID, time.Now()); refusal != nil {
				receipt.Detail = refusal.Error()
			}
		}
	}
	return receipt, err
}
func (d *Daemon) sentToInbox(addresses ...inbox.Address) {
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
func (d *Daemon) inboxAddressesOf(sessionID string) []inbox.Address {
	addresses := []inbox.Address{inbox.ToSession(sessionID)}
	if member := d.crewMemberBoundTo(sessionID); member != "" {
		addresses = append(addresses, inbox.ToMember(member))
	}
	if d.isChiefOfStaffSession(sessionID) {
		addresses = append(addresses, inbox.ToChief())
	}
	return addresses
}
