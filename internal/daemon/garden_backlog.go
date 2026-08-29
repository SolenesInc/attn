package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
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
		automation, err := d.store.HasAutomationTicketProvenance(ticket.ID)
		if err != nil {
			return nil, err
		}
		if automation {
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
	seedSchema, err := d.seedsCollection()
	if err != nil {
		return "", err
	}
	noteSchema, err := d.notesCollection()
	if err != nil {
		return "", err
	}
	dispatchSchema, err := d.dispatchesCollection()
	if err != nil {
		return "", err
	}
	now := time.Now()
	var linked store.LegacyTicketSeedResult
	for attempt := 0; attempt < 3; attempt++ {
		seedID, err := d.mintSeedID()
		if err != nil {
			return "", err
		}
		noteID, err := d.mintNoteID()
		if err != nil {
			return "", err
		}
		seed := garden.Seed{ID: seedID, Title: title, Body: body, Status: garden.StatusPlanted,
			StepSlug: garden.StepSlug(title), Edges: []garden.Edge{}, Vars: []garden.Var{}}
		seedBody, err := seed.Encode()
		if err != nil {
			return "", err
		}
		note := garden.Note{ID: noteID, Seed: seedID, Kind: garden.NoteKindNote,
			Body: fmt.Sprintf("converted from backlog ticket `%s` at the garden cutover; the ticket is archived and still readable with `attn ticket show %s`", ticket.ID, ticket.ID)}
		noteBody, err := note.Encode()
		if err != nil {
			return "", err
		}
		spec := store.LegacyTicketSeedSpec{
			TicketID: ticket.ID, SeedID: seedID, SeedBody: seedBody, SeedTitle: title, SeedDescription: body,
			SeedFact:   documentChangedFact(garden.Namespace, garden.CollectionSeeds, seedID, false),
			SeedSchema: *seedSchema, NoteSchema: *noteSchema, DispatchSchema: *dispatchSchema,
			Notes: []store.LegacyTicketSeedNote{{
				ID: noteID, Body: noteBody,
				Fact: documentChangedFact(garden.Namespace, garden.CollectionNotes, noteID, false),
			}}, SessionIDs: []string{ticket.Assignee, ticket.ResumeSessionID},
			SourceKind: "backlog", EvidenceFingerprint: legacyTicketSeedFingerprint(ticket, "backlog"),
			OriginalTerminalState: ticket.Status, CreatedAt: now,
		}
		linked, err = d.store.EnsureLegacyTicketSeed(spec)
		if err != nil && docstore.IsConflict(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if linked.Result == "created" {
			d.announceLegacyTicketSeedWrites(spec, linked)
		}
		break
	}
	if linked.SeedID == "" {
		return "", fmt.Errorf("ticket %s has no safe one-to-one seed: %s", ticket.ID, linked.Result)
	}
	if linked.Result == "created" {
		d.publishFact(FactGardenPlanted, linked.SeedID, nil)
		d.publishFact(FactGardenNoted, linked.SeedID, nil)
	}
	// Closed before archived: archiving is only offered to a closed ticket.
	if _, _, err := d.store.SetTicketStatusWithOptions(
		ticket.ID, store.TicketStatusDone, store.TicketAuthorAttn,
		fmt.Sprintf("converted to seed %s at the garden cutover; the work continues there", linked.SeedID),
		mirrorTicketMutationOptions(), now,
	); err != nil {
		return "", fmt.Errorf("close %s after planting %s: %w", ticket.ID, linked.SeedID, err)
	}
	if err := d.store.ArchiveTicket(ticket.ID, now); err != nil {
		return "", fmt.Errorf("archive %s after planting %s: %w", ticket.ID, linked.SeedID, err)
	}
	d.publishTicketFact(FactTicketChanged, ticket.ID)
	return linked.SeedID, nil
}

func (d *Daemon) announceLegacyTicketSeedWrites(spec store.LegacyTicketSeedSpec, result store.LegacyTicketSeedResult) {
	facts := make([]store.BusEvent, 0, len(spec.Notes)+1)
	facts = append(facts, spec.SeedFact)
	for _, note := range spec.Notes {
		facts = append(facts, note.Fact)
	}
	if len(result.Seqs) != len(facts) {
		d.logf("garden: recovered seed %s committed %d document fact(s), want %d", result.SeedID, len(result.Seqs), len(facts))
		return
	}
	for i, fact := range facts {
		d.announceCommittedWrite(fact, result.Seqs[i])
	}
}
