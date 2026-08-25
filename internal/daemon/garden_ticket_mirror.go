package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

// This mirror is the inverse of mirrorStatusOntoSeed. The two cannot loop: both
// write through store/log helpers rather than through each other's handler.

func seedMoveTicketStatus(verb garden.Verb) (store.TicketStatus, bool) {
	switch verb {
	case garden.VerbTend, garden.VerbReplant:
		return store.TicketStatusWorking, true
	case garden.VerbPark:
		return store.TicketStatusBlocked, true
	case garden.VerbHarvest:
		return store.TicketStatusDone, true
	case garden.VerbWither:
		return store.TicketStatusFailed, true
	default:
		return "", false
	}
}

func (d *Daemon) mirrorSeedMoveOntoTicket(sessionID, seedID string, verb garden.Verb, reason string) {
	ticket, ok := d.mirrorTargetTicket(sessionID, seedID)
	if !ok {
		return
	}
	status, ok := seedMoveTicketStatus(verb)
	if !ok {
		return
	}
	if ticket.Status == status {
		return
	}
	comment := strings.TrimSpace(reason)
	if comment == "" {
		comment = string(verb) + "ed " + seedID
	}
	d.deliveryMu.Lock()
	updated, _, err := d.store.SetTicketStatusWithOptions(
		ticket.ID, status, d.ticketActorIdentity(sessionID), comment,
		mirrorTicketMutationOptions(), time.Now(),
	)
	d.deliveryMu.Unlock()
	if err != nil {
		d.logf("garden: mirroring %s of %s onto ticket %s: %v", verb, seedID, ticket.ID, err)
		return
	}
	// No observer nudge: the tender already wrote the same report in the seed's log.
	d.publishTicketFact(FactTicketStatusChanged, updated.ID)
}

func (d *Daemon) mirrorSeedNoteOntoTicket(sessionID, seedID, body string) {
	ticket, ok := d.mirrorTargetTicket(sessionID, seedID)
	if !ok {
		return
	}
	if strings.TrimSpace(body) == "" {
		return
	}
	d.deliveryMu.Lock()
	_, _, err := d.store.AddTicketCommentWithOptions(
		ticket.ID, d.ticketActorIdentity(sessionID), body,
		mirrorTicketMutationOptions(), time.Now(),
	)
	d.deliveryMu.Unlock()
	if err != nil {
		d.logf("garden: mirroring a note on %s onto ticket %s: %v", seedID, ticket.ID, err)
		return
	}
	d.publishTicketFact(FactTicketCommented, ticket.ID)
}

func (d *Daemon) mirrorTargetTicket(sessionID, seedID string) (*store.Ticket, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.TrimSpace(seedID) == "" {
		return nil, false
	}
	if bound, ok := d.gardenDispatchCrown(sessionID); !ok || bound != seedID {
		return nil, false
	}
	ticket, err := d.store.ActiveTicketForSession(sessionID)
	if err != nil {
		d.logf("garden: resolving the ticket bound to session %s: %v", sessionID, err)
		return nil, false
	}
	if ticket == nil || ticket.Assignee != sessionID {
		return nil, false
	}
	return ticket, true
}

// No observers and no attention key on purpose: the read-before-you-write gate would
// drop the echo exactly when the ticket has unread news on it.
func mirrorTicketMutationOptions() store.TicketMutationOptions {
	return store.TicketMutationOptions{}
}
