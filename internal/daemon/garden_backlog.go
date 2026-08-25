package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

// Idempotence is the archive: a converted ticket leaves the board and the pass reads
// only unarchived todos.

func (d *Daemon) convertBacklogTicketsToSeeds() {
	if d.store == nil {
		return
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return
	}
	pending, err := d.unboundBacklogTickets()
	if err != nil {
		d.logf("garden: reading the backlog to convert: %v", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	converted := 0
	for _, ticket := range pending {
		seedID, err := d.convertBacklogTicket(ticket)
		if err != nil {
			d.logf("garden: converting backlog ticket %s: %v", ticket.ID, err)
			continue
		}
		converted++
		d.logf("garden: converted backlog ticket %s to seed %s (%q)", ticket.ID, seedID, ticket.Title)
	}
	d.logf("garden: backlog conversion done: %d of %d unbound todo ticket(s) are seeds now", converted, len(pending))
}

func (d *Daemon) unboundBacklogTickets() ([]*store.Ticket, error) {
	tickets, err := d.store.ListTickets(store.TicketListFilter{Status: store.TicketStatusTodo})
	if err != nil {
		return nil, err
	}
	unbound := make([]*store.Ticket, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket == nil || strings.TrimSpace(ticket.Assignee) != "" {
			continue
		}
		unbound = append(unbound, ticket)
	}
	return unbound, nil
}

// The close and archive come last: a crash between them duplicates a seed, which a person
// can wither, where the other order loses the work outright.
func (d *Daemon) convertBacklogTicket(ticket *store.Ticket) (string, error) {
	title := strings.TrimSpace(ticket.Title)
	body := strings.TrimSpace(ticket.Description)
	if err := garden.ValidatePlant(title, body); err != nil {
		return "", err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return "", err
	}
	seed, _, err := d.mintAndPlant(*schema, garden.Seed{
		Title:    title,
		Body:     body,
		Status:   garden.StatusPlanted,
		StepSlug: garden.StepSlug(title),
		Edges:    []garden.Edge{},
		Vars:     []garden.Var{},
	})
	if err != nil {
		return "", err
	}
	if _, err := d.appendSeedNote(seed.ID,
		fmt.Sprintf("converted from backlog ticket `%s` at the garden cutover; the ticket is archived and still readable with `attn ticket show %s`", ticket.ID, ticket.ID),
		"", "", garden.NoteKindNote, nil,
	); err != nil {
		d.logf("garden: recording ticket %s as the origin of seed %s: %v", ticket.ID, seed.ID, err)
	}
	// Closed before archived: archiving is only offered to a closed ticket.
	now := time.Now()
	if _, _, err := d.store.SetTicketStatusWithOptions(
		ticket.ID, store.TicketStatusDone, store.TicketAuthorAttn,
		fmt.Sprintf("converted to seed %s at the garden cutover; the work continues there", seed.ID),
		mirrorTicketMutationOptions(), now,
	); err != nil {
		return "", fmt.Errorf("close %s after planting %s: %w", ticket.ID, seed.ID, err)
	}
	if err := d.store.ArchiveTicket(ticket.ID, now); err != nil {
		return "", fmt.Errorf("archive %s after planting %s: %w", ticket.ID, seed.ID, err)
	}
	d.publishTicketFact(FactTicketChanged, ticket.ID)
	return seed.ID, nil
}
