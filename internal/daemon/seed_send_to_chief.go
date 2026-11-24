package daemon

import (
	"net"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/who"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

func chiefSeedAssignmentNote(
	continuation *seedContinuation,
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
	return body.String()
}

func chiefSeedAssignmentPrompt(seedID string) string {
	return prompts.RenderText("chief", "seed-assignment", prompts.Values{"seed_id": seedID})
}

func (d *Daemon) deliverChiefSeedAssignment(chiefSessionID protocol.SessionID, seedID string) (protocol.AgentMsgStatus, string) {
	receipt, err := d.sendToInbox(inbox.Item{To: who.ToTenderOf(seedID), Kind: inbox.Notice, Text: chiefSeedAssignmentPrompt(seedID)})
	if err != nil {
		d.logf("seed send to Chief: queue %s for %s: %v", seedID, chiefSessionID, err)
		return protocol.AgentMsgStatusRefused, "Chief now tends the seed, but its inbox item could not be recorded; the assignment remains on the seed log"
	}
	if !receipt.Rang {
		return protocol.AgentMsgStatusQueued, receipt.Detail
	}
	return protocol.AgentMsgStatusNotified, "notified Chief"
}

func (d *Daemon) sendSeedToChief(msg *protocol.SeedSendToChiefMessage, profileID string, by who.Actor) (*protocol.SeedSendToChiefResult, error) {
	chief, ok := d.chiefParty(profileID)
	if !ok {
		return nil, protocol.Refuse(protocol.RefusalWrongState, "this profile has no Chief yet; attn creates it at the next daemon start")
	}
	chiefSessionID, _ := d.liveSessionOf(chief)
	seedID := strings.TrimSpace(msg.SeedID)
	guidance := strings.TrimSpace(protocol.Deref(msg.Guidance))
	if len(guidance) > garden.MaxNoteBytes/2 {
		return nil, protocol.Refuse(protocol.RefusalLimit, "chief guidance is %d bytes and the limit is %d", len(guidance), garden.MaxNoteBytes/2)
	}

	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	if garden.Closed(seed.Status) {
		return nil, protocol.Refuse(protocol.RefusalWrongState, "%s is %s; replant it before sending it to Chief", seed.ID, seed.Status)
	}
	if msg.ExpectedRev <= 0 || int(doc.Rev) != msg.ExpectedRev {
		return nil, protocol.Refuse(protocol.RefusalConflict, "%s changed since you opened it; refresh it before sending it to Chief", seed.ID)
	}
	unclaimed := seed
	unclaimed.Tender = who.Party{}
	outcome, err := garden.Transition(unclaimed, garden.VerbTend, garden.Ask{
		Actor: chief.Actor(), Claimant: chief,
	}, gardenStanding{daemon: d, profileID: seed.ProfileID}, nil)
	if err != nil {
		return nil, err
	}
	next := outcome.Seed
	if next.Status != seed.Status {
		next.StateChangedAt = formatGardenTime(d.gardenTime())
	}

	noteBody := chiefSeedAssignmentNote(d.continuationForSeed(seed), guidance)
	if err := garden.ValidateNote(noteBody); err != nil {
		return nil, err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	cause := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
	tended, err := gardenSeedLifecycleOccurrence(garden.VerbTend, next.ID, seedCause{session: cause, by: by, notifiedSession: chiefSessionID, directlyNotified: chief})
	if err != nil {
		return nil, err
	}
	d.lockGardenRoles()
	written, _, err := d.writeSeedChange(*schema, next, doc.Rev, seedChange{
		author: by, cause: cause,
		events: []seedEvent{{occurrence: tended, detail: "sent to the chief"}},
		entries: []seedMoveNote{{Note: garden.Note{
			Seed: next.ID, Kind: garden.NoteKindNote, Body: noteBody,
			Author: by,
		}}},
	})
	if err == nil {
		err = d.discardAllIneligibleGardenSeedBellsLocked()
	}
	d.unlockGardenRoles()
	if err != nil {
		if docstore.IsConflict(err) {
			return nil, protocol.Refuse(protocol.RefusalConflict, "%s changed while it was being sent to Chief; refresh the garden", seed.ID)
		}
		return nil, err
	}
	status, detail := d.deliverChiefSeedAssignment(chiefSessionID, seed.ID)

	wire := d.seedWire(next, written)
	d.decorateSeedContinuation(&wire, next)
	return &protocol.SeedSendToChiefResult{
		Seed: wire, ChiefSessionID: chiefSessionID, DeliveryStatus: status, Detail: detail,
	}, nil
}

func (d *Daemon) handleSeedSendToChiefWS(client *wsClient, msg *protocol.SeedSendToChiefMessage) {
	response := protocol.SeedSendToChiefResultMessage{
		Event: protocol.EventSeedSendToChiefResult, RequestID: protocol.Deref(msg.RequestID),
	}
	result, err := d.sendSeedToChief(msg, d.profileForClient(client), client.caller)
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.Success = true
		response.Result = result
	}
	d.sendToClient(client, response)
}

func (d *Daemon) handleSeedSendToChief(conn net.Conn, msg *protocol.SeedSendToChiefMessage, by who.Actor) {
	result, err := d.sendSeedToChief(msg, protocol.Deref(msg.ProfileID), by)
	if err != nil {
		d.sendGardenError(conn, "send-to-chief", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, SeedSendToChiefResult: result})
}
