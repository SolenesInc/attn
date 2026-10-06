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
	ask, sessionID := d.seedTransitionAsk(msg)
	if harvestWhenRequested(msg) {
		seed, doc, err := d.applyHarvestWhenRequest(msg, verb, ask, protocol.SessionID(sessionID))
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
	seed, doc, _, err := d.applySeedTransitionDetailedAtRevision(
		msg.SeedID, verb, ask, protocol.Deref(msg.Comment), expectedRev)
	if err != nil {
		fail(err)
		return
	}
	if err := d.resolveGardenReviewAction(msg.Review, msg.SeedID, string(verb)); err != nil {
		d.logf("Garden review: settle %s after %s: %v", msg.SeedID, verb, err)
	}
	wire := d.seedTransitionWire(seed, doc)

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
	authorSession := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
	note, err := d.appendSeedNote(
		msg.SeedID,
		msg.Body,
		authorSession,
		protocol.Deref(msg.Member),
		protocol.Deref(msg.Kind),
		artifactFromProtocol(msg.Artifact),
		protocol.Deref(msg.Ring), authorSession,
	)
	if err != nil {
		fail(err)
		return
	}

	result.Note = &note
	result.Success = true
	d.sendToClient(client, result)
}
