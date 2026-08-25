package daemon

import (
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/ticketnotify"
)

// The forward, inverse, and attention views of one mapping: they are defined
// together because a role they disagree on is an identity nothing delivers to.

// In precedence order: a member comes first, its cursors and clock outliving its days.
func (d *Daemon) ticketDurableIdentitiesForSession(sessionID string) []string {
	var identities []string
	if member := d.crewMemberBoundTo(sessionID); member != "" {
		identities = append(identities, store.TicketMemberIdentity(member))
	}
	if d.isChiefOfStaffSession(sessionID) {
		identities = append(identities, store.TicketRoleIdentity(store.TicketRoleChiefOfStaff))
	}
	return identities
}

func (d *Daemon) ticketSessionForIdentity(identity string) string {
	if identity == store.TicketRoleIdentity(store.TicketRoleChiefOfStaff) {
		return d.chiefOfStaffSessionID()
	}
	if memberID, ok := store.ParseTicketMemberIdentity(identity); ok {
		member, _, err := d.crewMember(memberID)
		if err != nil || !d.crewBindingLive(member) {
			return ""
		}
		return member.BindingSession
	}
	return identity
}

func (d *Daemon) ticketActorIdentity(sessionID string) string {
	for _, identity := range d.ticketDurableIdentitiesForSession(sessionID) {
		if _, ok := store.ParseTicketMemberIdentity(identity); ok {
			return identity
		}
	}
	return sessionID
}

// AuthorID is the acting identity, so a member never receives its own event;
// DeliveryID is the concrete session, the only thing that can be nudged.
func (d *Daemon) ticketObserversForSession(sessionID string) []ticketnotify.Observer {
	authorID := d.ticketActorIdentity(sessionID)
	durable := d.ticketDurableIdentitiesForSession(sessionID)
	observers := make([]ticketnotify.Observer, 0, len(durable)+1)
	if _, member := store.ParseTicketMemberIdentity(authorID); !member {
		observers = append(observers, ticketnotify.Observer{ID: sessionID, AuthorID: authorID, DeliveryID: sessionID})
	}
	for _, roleIdentity := range durable {
		observers = append(observers, ticketnotify.Observer{
			ID:         roleIdentity,
			AuthorID:   authorID,
			DeliveryID: sessionID,
		})
	}
	return observers
}

func (d *Daemon) ticketAttentionKey(sessionID string) string {
	if roles := d.ticketDurableIdentitiesForSession(sessionID); len(roles) > 0 {
		return roles[0]
	}
	return sessionID
}
