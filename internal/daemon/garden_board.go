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
	ask, sessionID, err := d.seedTransitionAsk(msg, verb, client.caller)
	if err != nil {
		fail(err)
		return
	}
	if harvestWhenRequested(msg) {
		seed, doc, _, err := d.applyHarvestWhenRequest(msg, verb, ask, protocol.SessionID(sessionID))
		if err != nil {
			fail(err)
			return
		}
		wire := d.seedWire(seed, doc)
		result.Seed = &wire
		result.Success = true
		d.sendToClient(client, result)
		return
	}
	seed, doc, _, err := d.applySeedTransitionDetailed(
		msg.SeedID, verb, ask, protocol.Deref(msg.Comment))
	if err != nil {
		fail(err)
		return
	}
	wire := d.seedWire(seed, doc)

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
	authorSession := protocol.TrimID(protocol.Deref(msg.SourceSessionID))
	note, err := d.appendSeedNote(
		msg.SeedID,
		msg.Body,
		client.caller,
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
