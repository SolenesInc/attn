package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) replantStrandedTickets() {
	if d.store == nil {
		return
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return
	}
	stranded, err := d.store.StrandedTickets()
	if err != nil {
		d.logf("garden: reading the tickets stranded on the retired board: %v", err)
		return
	}
	if len(stranded) == 0 {
		return
	}
	replanted := 0
	for _, ticket := range stranded {
		if d.replantStrandedTicketByID(ticket.ID) {
			replanted++
		}
	}
	d.logf("garden: %d of %d stranded ticket(s) are seeds now", replanted, len(stranded))
}

func (d *Daemon) replantStrandedTicketByID(ticketID string) bool {
	if d.store == nil {
		return false
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return false
	}
	ticket, err := d.store.GetTicket(ticketID)
	if err != nil || ticket == nil {
		if err != nil {
			d.logf("garden: reading stranded ticket %s: %v", ticketID, err)
		}
		return false
	}
	if !isStrandedTicket(ticket) {
		return false
	}
	seedID, err := d.replantStrandedTicket(ticket)
	if err != nil {
		d.logf("garden: replanting stranded ticket %s: %v", ticket.ID, err)
		return false
	}
	d.logf("garden: replanted stranded ticket %s as seed %s (%q)", ticket.ID, seedID, ticket.Title)
	return true
}

func isStrandedTicket(ticket *store.Ticket) bool {
	if ticket == nil || ticket.ArchivedAt != nil || strings.TrimSpace(ticket.AutomationRunID) != "" {
		return false
	}
	return ticket.Status == store.TicketStatusCrashed || ticket.Status == store.TicketStatusFailed
}

// The plant comes first and the archive last: a crash between them duplicates a
// seed, which a person can wither; the other order loses the work.
func (d *Daemon) replantStrandedTicket(ticket *store.Ticket) (string, error) {
	title := strings.TrimSpace(ticket.Title)
	body := strings.TrimSpace(ticket.Description)
	if err := garden.ValidatePlant(title, body); err != nil {
		return "", err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return "", err
	}
	plant := garden.Seed{
		Title:    title,
		Body:     body,
		Status:   garden.StatusGrowing,
		StepSlug: garden.StepSlug(title),
		Edges:    []garden.Edge{},
		Vars:     []garden.Var{},
	}
	if ticket.Status == store.TicketStatusFailed {
		plant.Status = garden.StatusWithered
		plant.Reason = "reported failed before the garden; replanted from ticket " + ticket.ID
	} else {
		plant.TenderSession = strings.TrimSpace(ticket.Assignee)
	}
	seed, _, err := d.mintAndPlant(*schema, plant)
	if err != nil {
		return "", err
	}
	if _, err := d.appendSeedNote(seed.ID, strandedProvenanceNote(ticket), "", "", garden.NoteKindNote, nil); err != nil {
		d.logf("garden: recording ticket %s as the origin of seed %s: %v", ticket.ID, seed.ID, err)
	}
	now := time.Now()
	if _, _, err := d.store.SetTicketStatusWithOptions(
		ticket.ID, store.TicketStatusDone, store.TicketAuthorAttn,
		fmt.Sprintf("replanted as seed %s; the work continues in the garden", seed.ID),
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

func strandedProvenanceNote(ticket *store.Ticket) string {
	origin := fmt.Sprintf("replanted from ticket `%s`, which crashed: its session ended mid-flight without reporting.", ticket.ID)
	if ticket.Status == store.TicketStatusFailed {
		origin = fmt.Sprintf("replanted from ticket `%s`, whose agent reported it failed.", ticket.ID)
	}
	lines := []string{origin}
	if session := strings.TrimSpace(ticket.Assignee); session != "" {
		lines = append(lines, fmt.Sprintf("It ran in session `%s`.", session))
	}
	if verdict := reconcileVerdictComment(ticket); verdict != "" {
		lines = append(lines, "", verdict)
	}
	lines = append(lines, "", fmt.Sprintf("The ticket is archived and still readable in full with `attn ticket show %s`.", ticket.ID))
	return strings.Join(lines, "\n")
}

func reconcileVerdictComment(ticket *store.Ticket) string {
	verdict := ""
	for _, entry := range ticket.Activity {
		if entry.Kind != store.TicketActivityComment {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(entry.Comment), ticketReconcileCommentPrefix) {
			verdict = strings.TrimSpace(entry.Comment)
		}
	}
	return verdict
}
