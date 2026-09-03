package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func chiefSeedAssignmentNote(
	continuation *seedContinuation,
	item garden.ReviewItem,
	guidance string,
) string {
	var body strings.Builder
	body.WriteString("Sent to Chief to decide the next working context.")
	if continuation != nil {
		execution := continuation.Execution
		if value := strings.TrimSpace(execution.Cwd); value != "" {
			body.WriteString("\n\nSaved folder: `")
			body.WriteString(value)
			body.WriteString("`")
		}
		if value := strings.TrimSpace(execution.RepositoryRoot); value != "" {
			body.WriteString("\nSaved repository: `")
			body.WriteString(value)
			body.WriteString("`")
		}
		if value := strings.TrimSpace(execution.Branch); value != "" {
			body.WriteString("\nSaved branch: `")
			body.WriteString(value)
			body.WriteString("`")
		}
		if value := strings.TrimSpace(continuation.PlacementReason); value != "" {
			body.WriteString("\nAutomatic Handover: ")
			body.WriteString(value)
		}
	}
	if value := strings.TrimSpace(guidance); value != "" {
		body.WriteString("\n\nUser guidance:\n")
		body.WriteString(value)
	}
	if item.Recommendation != "" {
		body.WriteString("\n\nAdvisor suggested ")
		body.WriteString(item.Recommendation)
		if value := strings.TrimSpace(item.Explanation); value != "" {
			body.WriteString(": ")
			body.WriteString(value)
		}
	}
	return body.String()
}

func chiefSeedAssignmentPrompt(seedID string) string {
	return fmt.Sprintf(
		"The user sent seed `%s` to you to decide how it should continue. You are now its tender. Read the seed and its latest log note with `attn seed show %s`, then choose the working context and hand it over as appropriate.",
		seedID, seedID,
	)
}

func (d *Daemon) deliverChiefSeedAssignment(chiefSessionID, seedID string) (protocol.AgentMsgStatus, string) {
	now := time.Now()
	itemID := uuid.NewString()
	prompt := chiefSeedAssignmentPrompt(seedID)
	delivery, err := d.store.EnqueueMaintenancePrompt(itemID, chiefSessionID, prompt, now)
	if err != nil {
		d.logf("seed send to Chief: queue %s for %s: %v", seedID, chiefSessionID, err)
		return protocol.AgentMsgStatusRefused, "Chief now tends the seed, but its inbox item could not be recorded; the assignment remains on the seed log"
	}
	if err := d.deliverAgentMailboxItem(delivery); err != nil {
		return protocol.AgentMsgStatusQueued, agentMessageQueuedDetail(err)
	}
	return protocol.AgentMsgStatusNotified, "notified Chief"
}

func (d *Daemon) sendSeedToChief(msg *protocol.SeedSendToChiefMessage) (*protocol.SeedSendToChiefResult, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	chiefSessionID := d.chiefOfStaffSessionID()
	if chiefSessionID == "" || d.store.Get(chiefSessionID) == nil {
		return nil, fmt.Errorf("no Chief is available")
	}
	seedID := strings.TrimSpace(msg.SeedID)
	guidance := strings.TrimSpace(protocol.Deref(msg.Guidance))
	if len(guidance) > garden.MaxNoteBytes/2 {
		return nil, fmt.Errorf("chief guidance is %d bytes and the limit is %d", len(guidance), garden.MaxNoteBytes/2)
	}

	var reviewItem garden.ReviewItem
	if msg.Review != nil {
		item, err := d.validateGardenReviewAction(msg.Review, seedID, "send_to_chief")
		if err != nil {
			return nil, err
		}
		reviewItem = item
		if item.SeedRev != int64(msg.ExpectedRev) {
			return nil, fmt.Errorf("%s changed since you reviewed it; refresh the garden", seedID)
		}
	}

	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	if garden.Closed(seed.Status) {
		return nil, fmt.Errorf("%s is %s; replant it before sending it to Chief", seed.ID, seed.Status)
	}
	if msg.ExpectedRev <= 0 || int(doc.Rev) != msg.ExpectedRev ||
		seed.TenderSession != strings.TrimSpace(msg.ExpectedTenderSession) ||
		seed.TenderMember != strings.TrimSpace(msg.ExpectedTenderMember) {
		return nil, fmt.Errorf("%s changed since you opened it; refresh it before sending it to Chief", seed.ID)
	}
	unclaimed := seed
	unclaimed.TenderSession, unclaimed.TenderMember = "", ""
	next, err := garden.Transition(unclaimed, garden.VerbTend, garden.Ask{
		Actor: garden.Tender{Session: chiefSessionID},
	}, d.sessionExists)
	if err != nil {
		return nil, err
	}
	if next.Status != seed.Status {
		next.StateChangedAt = formatGardenTime(d.gardenTime())
	}

	noteBody := chiefSeedAssignmentNote(d.continuationForSeed(seed), reviewItem, guidance)
	if err := garden.ValidateNote(noteBody); err != nil {
		return nil, err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	written, notes, err := d.writeSeedMoveWithNotes(*schema, next, doc.Rev, FactGardenTended, []garden.Note{{
		Seed: next.ID, Kind: garden.NoteKindNote, Body: noteBody,
		AuthorSession: strings.TrimSpace(protocol.Deref(msg.SourceSessionID)),
	}})
	if err != nil {
		if docstore.IsConflict(err) {
			return nil, fmt.Errorf("%s changed while it was being sent to Chief; refresh the garden", seed.ID)
		}
		return nil, err
	}
	if len(notes) > 0 {
		d.mirrorSeedNoteOntoTicket(protocol.Deref(msg.SourceSessionID), seed.ID, notes[0].Body)
	}
	if err := d.resolveGardenReviewAction(msg.Review, seed.ID, "send_to_chief"); err != nil {
		d.logf("Garden review: settle %s after Send to Chief: %v", seed.ID, err)
	}
	d.ringSeedActivity(seed.ID, gardenRingEvents[garden.VerbTend], chiefSessionID, protocol.Deref(msg.SourceSessionID))
	status, detail := d.deliverChiefSeedAssignment(chiefSessionID, seed.ID)

	wire := seedToProtocol(next, written, false)
	d.decorateSeedContinuation(&wire, next)
	if read, readErr := d.readGarden(); readErr == nil {
		wire.Ready = read.ready[next.ID]
		if progress, ok := read.progress(next.ID); ok {
			wire.PlotProgress = progress
		}
	}
	return &protocol.SeedSendToChiefResult{
		Seed: wire, ChiefSessionID: chiefSessionID, DeliveryStatus: status, Detail: detail,
	}, nil
}

func (d *Daemon) handleSeedSendToChief(conn net.Conn, msg *protocol.SeedSendToChiefMessage) {
	result, err := d.sendSeedToChief(msg)
	if err != nil {
		d.sendGardenError(conn, "send-to-chief", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedSendToChiefResult: result})
}

func (d *Daemon) handleSeedSendToChiefWS(client *wsClient, msg *protocol.SeedSendToChiefMessage) {
	response := protocol.SeedSendToChiefResultMessage{
		Event: protocol.EventSeedSendToChiefResult, RequestID: protocol.Deref(msg.RequestID),
	}
	result, err := d.sendSeedToChief(msg)
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Success = true
		response.Result = result
	}
	d.sendToClient(client, response)
}
