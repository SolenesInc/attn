package daemon

import (
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

// Prototype: docs/plans/2026-08-20-garden-kanban-board-prototype.md.

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
	seed, doc, notes, err := d.applySeedTransitionDetailed(
		msg.SeedID, verb, ask, protocol.Deref(msg.Comment))
	if err != nil {
		fail(err)
		return
	}
	for _, note := range notes.all() {
		d.mirrorSeedNoteOntoTicket("", seed.ID, note.Body)
	}
	wire := seedToProtocol(seed, doc, false)
	if read, err := d.readGarden(); err == nil {
		wire.Ready = read.ready[seed.ID]
		if progress, ok := read.progress(seed.ID); ok {
			wire.PlotProgress = progress
		}
	}
	d.mirrorSeedMoveOntoTicket("", seed.ID, verb, protocol.Deref(msg.Reason))
	d.ringSeedActivity(seed.ID, gardenRingEvents[verb], "")
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
