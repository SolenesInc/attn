package daemon

import (
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
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
	msg.SourceSessionID = nil
	msg.ProfileID = protocol.Ptr(client.selectedProfile())
	r := who.RequestFromApp(client.selectedProfile())
	ask := garden.Ask{By: r.Actor(), Reason: protocol.Deref(msg.Reason), Force: protocol.Deref(msg.Force)}
	sessionID, _ := r.AskingSession()
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
	seed, doc, _, err := d.applySeedMove(
		msg.SeedID, verb, d.seedMoveRequest(msg, verb), protocol.Deref(msg.Comment), expectedRev)
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
	r := who.RequestFromApp(client.selectedProfile())
	note, err := d.appendSeedNote(
		msg.SeedID,
		msg.Body,
		r.Actor(),
		protocol.Deref(msg.Kind),
		artifactFromProtocol(msg.Artifact),
		protocol.Deref(msg.Ring),
	)
	if err != nil {
		fail(err)
		return
	}

	result.Note = &note
	result.Success = true
	d.sendToClient(client, result)
}
