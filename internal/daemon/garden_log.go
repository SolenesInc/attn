package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type seedEvent struct {
	occurrence seedEvents.Occurrence
	detail     string
}

func plainSeedEvents(occurrences ...seedEvents.Occurrence) []seedEvent {
	events := make([]seedEvent, len(occurrences))
	for i, occurrence := range occurrences {
		events[i] = seedEvent{occurrence: occurrence}
	}
	return events
}

type seedChange struct {
	author  who.Actor
	cause   protocol.SessionID
	born    []string
	events  []seedEvent
	entries []seedMoveNote
}

type seedChangeLog struct {
	commits []store.DocumentCommit
	events  []store.BusEvent
	entries []garden.Note
}

// seedChangeLog is the one way a seed change reaches the event log: the change's events, the log
// entries their rules write and the caller's own entries, each entry with one NoteAdded naming it.
func (d *Daemon) seedChangeLog(change seedChange) (seedChangeLog, error) {
	entries := append([]seedMoveNote(nil), change.entries...)
	moveOn := map[string]int{}
	occurrences := make([]seedEvents.Occurrence, 0, len(change.events)+len(entries))
	for _, event := range change.events {
		occurrences = append(occurrences, event.occurrence)
		rendered, err := gardenSeedEventModel.Entries(event.occurrence, event.detail)
		if err != nil {
			return seedChangeLog{}, err
		}
		for _, entry := range rendered {
			if slices.Contains(change.born, entry.Seed) {
				continue
			}
			if index, ok := moveOn[entry.Seed]; ok && entry.Kind == garden.NoteKindMove {
				entries[index].Body += "; " + entry.Body
				continue
			}
			if entry.Kind == garden.NoteKindMove {
				moveOn[entry.Seed] = len(entries)
			}
			entries = append(entries, seedMoveNote{
				Note: garden.Note{
					Seed: entry.Seed, Kind: entry.Kind, Body: entry.Body,
					Author: change.author,
				},
				CausedBySessionID: change.cause,
			})
		}
	}
	var noteSchema *docstore.CollectionSchema
	if len(entries) > 0 {
		var err error
		if noteSchema, err = d.notesCollection(); err != nil {
			return seedChangeLog{}, err
		}
	}
	log := seedChangeLog{
		commits: make([]store.DocumentCommit, len(entries)),
		entries: make([]garden.Note, len(entries)),
	}
	for i, entry := range entries {
		var err error
		if err = garden.ValidateNote(entry.Body); err != nil {
			return seedChangeLog{}, err
		}
		if entry.ID == "" {
			if entry.ID, err = d.mintNoteID(); err != nil {
				return seedChangeLog{}, err
			}
		}
		body, err := entry.Note.Encode()
		if err != nil {
			return seedChangeLog{}, err
		}
		absent := docstore.ExpectAbsent
		log.commits[i] = store.DocumentCommit{
			Write: store.DocumentWrite{Schema: *noteSchema, ID: entry.ID, Body: body, Expected: &absent},
			Fact:  documentChangedFact(garden.Namespace, garden.CollectionNotes, entry.ID, false),
		}
		log.entries[i] = entry.Note
		added, err := seedEvents.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.NoteAdded, entry.Seed, seedEvents.NoteAddedPayload{
			NoteID: entry.ID, AttentionRequested: entry.AttentionRequested, CausedBySessionID: protocol.TrimID(entry.CausedBySessionID), CausedBy: entry.Author.String(),
		})
		if err != nil {
			return seedChangeLog{}, err
		}
		occurrences = append(occurrences, added)
	}
	log.events = make([]store.BusEvent, len(occurrences))
	for i, occurrence := range occurrences {
		event, err := gardenSeedEventModel.Encode(occurrence)
		if err != nil {
			return seedChangeLog{}, err
		}
		log.events[i] = store.BusEvent{
			Name: event.Name, Subject: event.Subject, Payload: string(event.Payload), Source: "garden",
		}
	}
	return log, nil
}

func (d *Daemon) seedChangeLogWire(l seedChangeLog, now time.Time) []protocol.SeedNote {
	wire := make([]protocol.SeedNote, len(l.entries))
	for i, entry := range l.entries {
		wire[i] = d.noteToProtocol(entry, docstore.Document{CreatedAt: now})
	}
	return wire
}

// writeSeedChange commits the seed document with its change's events and log entries. It returns
// every entry written, the caller's own first and in the order given.
func (d *Daemon) writeSeedChange(
	schema docstore.CollectionSchema, seed garden.Seed, expected int64, change seedChange,
) (docstore.Document, []protocol.SeedNote, error) {
	var written docstore.Document
	var entries []protocol.SeedNote
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var err error
		written, entries, err = d.writeSeedChangeProtected(protection, schema, seed, expected, change)
		return err
	})
	return written, entries, err
}

func (d *Daemon) writeSeedChangeProtected(
	_ foregroundCleanupProtection,
	schema docstore.CollectionSchema, seed garden.Seed, expected int64, change seedChange,
) (docstore.Document, []protocol.SeedNote, error) {
	seedBody, err := seed.Encode()
	if err != nil {
		return docstore.Document{}, nil, err
	}
	const mintAttempts = 3
	var lastErr error
	for range mintAttempts {
		log, err := d.seedChangeLog(change)
		if err != nil {
			return docstore.Document{}, nil, err
		}
		commits := append([]store.DocumentCommit{{
			Write: store.DocumentWrite{Schema: schema, ID: seed.ID, Body: seedBody, Expected: &expected},
			Fact:  documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false),
		}}, log.commits...)
		now := d.gardenTime()
		written, eventSeqs, err := d.store.CommitDocumentWritesWithEvents(commits, log.events, now)
		if err != nil {
			var conflict *docstore.ConflictError
			if errors.As(err, &conflict) && conflict.Namespace == garden.Namespace &&
				conflict.Collection == garden.CollectionNotes {
				lastErr = err
				continue
			}
			return docstore.Document{}, nil, err
		}
		d.coalesceSnapshots(func() {
			for i, commit := range commits {
				d.announceCommittedWrite(commit.Fact, written[i].Seq)
			}
			announceGardenSeedEvents(d, eventSeqs)
		})
		seedDoc, found, readErr := d.store.GetDocument(schema, seed.ID)
		if readErr != nil || !found {
			seedDoc = &docstore.Document{ID: seed.ID, Body: seedBody, Rev: written[0].Rev, CreatedAt: now, UpdatedAt: now}
		}
		return *seedDoc, d.seedChangeLogWire(log, now), nil
	}
	return docstore.Document{}, nil, fmt.Errorf(
		"minted %d note ids and every one was taken, which a working random source does not do: %v", mintAttempts, lastErr)
}

func (c seedChange) occurrences() []seedEvents.Occurrence {
	occurrences := make([]seedEvents.Occurrence, len(c.events))
	for i, event := range c.events {
		occurrences[i] = event.occurrence
	}
	return occurrences
}

func joinEntryDetail(detail, more string) string {
	if detail == "" {
		return more
	}
	return detail + " — " + more
}
