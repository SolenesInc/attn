package daemon

import (
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) handleSeedTransitionWS(client *wsClient, msg *protocol.SeedTransitionMessage) {
	result := protocol.SeedTransitionResultMessage{
		Event:     protocol.EventSeedTransitionResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
	}
	verb, err := garden.ParseVerb(msg.Verb)
	if err != nil {
		fail(err)
		return
	}
	if err := d.requireHome(garden.Surface); err != nil {
		fail(err)
		return
	}
	ask := garden.Ask{Reason: protocol.Deref(msg.Reason), Force: protocol.Deref(msg.Force)}
	if harvestWhenRequested(msg) {
		seed, doc, err := d.applyHarvestWhenRequest(msg, verb, ask, "")
		if err != nil {
			fail(err)
			return
		}
		wire := d.seedTransitionWire(seed, doc)
		result.Seed = &wire
		result.Success = true
		d.sendToClient(client, result)
		return
	}
	expectedRev := int64(0)
	if msg.Review != nil {
		item, reviewErr := d.validateGardenReviewAction(msg.Review, msg.SeedID, string(verb))
		if reviewErr != nil {
			fail(reviewErr)
			return
		}
		expectedRev = item.SeedRev
	}
	seed, doc, notes, err := d.applySeedTransitionDetailedAtRevision(
		msg.SeedID, verb, ask, protocol.Deref(msg.Comment), expectedRev)
	if err != nil {
		fail(err)
		return
	}
	if err := d.resolveGardenReviewAction(msg.Review, msg.SeedID, string(verb)); err != nil {
		d.logf("Garden review: settle %s after %s: %v", msg.SeedID, verb, err)
	}
	for _, note := range notes.all() {
		d.mirrorSeedNoteOntoTicket("", seed.ID, note.Body)
	}
	wire := d.seedTransitionWire(seed, doc)
	d.mirrorSeedMoveOntoTicket("", seed.ID, verb, protocol.Deref(msg.Reason))
	d.ringSeedActivity(seed.ID, gardenRingEvents[verb], "")
	if garden.Closed(seed.Status) {
		unblocked, _ := d.seedUnblocked(seed.ID)
		d.ringSeedUnblocked(unblocked)
	}
	result.Seed = &wire
	result.Success = true
	d.sendToClient(client, result)
}

func (d *Daemon) handleSeedNoteWS(client *wsClient, msg *protocol.SeedNoteMessage) {
	result := protocol.SeedNoteResultMessage{
		Event:     protocol.EventSeedNoteResult,
		RequestID: protocol.Deref(msg.RequestID),
	}
	fail := func(err error) {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
	}
	if err := d.requireHome(garden.Surface); err != nil {
		fail(err)
		return
	}
	note, err := d.appendSeedNote(
		msg.SeedID,
		msg.Body,
		"",
		protocol.Deref(msg.Member),
		protocol.Deref(msg.Kind),
		artifactFromProtocol(msg.Artifact),
	)
	if err != nil {
		fail(err)
		return
	}
	d.mirrorSeedNoteOntoTicket("", msg.SeedID, note.Body)
	if protocol.Deref(msg.Ring) {
		d.ringSeedActivity(msg.SeedID, "note", "")
	}
	result.Note = &note
	result.Success = true
	d.sendToClient(client, result)
}
