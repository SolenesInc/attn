package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
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
	automation, err := d.store.HasAutomationTicketProvenance(ticket.ID)
	if err != nil {
		d.logf("garden: checking Automation provenance for stranded ticket %s: %v", ticket.ID, err)
		return false
	}
	if automation {
		return false
	}
	link, err := d.store.LegacyTicketSeedLink(ticket.ID)
	if err != nil {
		d.logf("garden: checking the seed linked to stranded ticket %s: %v", ticket.ID, err)
		return false
	}
	if link != nil {
		return true
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

func (d *Daemon) replantStrandedTicket(ticket *store.Ticket) (string, error) {
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
		seed := garden.Seed{
			ID: seedID, Title: title, Body: body, Status: garden.StatusWithered,
			StepSlug: garden.StepSlug(title), Edges: []garden.Edge{}, Vars: []garden.Var{},
			Reason:          "recovered from legacy ticket " + ticket.ID,
			ResumeSessionID: ticket.ResumeSessionID, ResumeCwd: ticket.Cwd, ResumeAgent: ticket.LastAgentID,
		}
		seedBody, err := seed.Encode()
		if err != nil {
			return "", err
		}
		note := garden.Note{ID: noteID, Seed: seedID, Kind: garden.NoteKindNote, Body: strandedProvenanceNote(ticket)}
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
			SourceKind: "stranded", EvidenceFingerprint: legacyTicketSeedFingerprint(ticket, "stranded"),
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
	return linked.SeedID, nil
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
	lines = append(lines, "", fmt.Sprintf("The legacy ticket remains unchanged and readable in full with `attn ticket show %s`.", ticket.ID))
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
