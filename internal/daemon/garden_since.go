package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
)

type gardenBookmark struct {
	earliest int64
	head     int64
}

func (d *Daemon) takeGardenBookmark() (gardenBookmark, error) {
	earliest, head, err := d.store.BusBounds()
	if err != nil {
		return gardenBookmark{}, fmt.Errorf("read the event log head: %w", err)
	}
	return gardenBookmark{earliest: earliest, head: head}, nil
}

func (b gardenBookmark) expired(after int64) bool {
	return b.earliest > 0 && after < b.earliest-1
}

func (d *Daemon) expiredSince(mark gardenBookmark) *protocol.SeedSinceExpired {
	return &protocol.SeedSinceExpired{
		Earliest:         int(mark.earliest),
		RetentionSeconds: int(d.eventBus.Retention().Seconds()),
	}
}

func (d *Daemon) seedSince(read gardenRead, crownID string, after int64, mark gardenBookmark) (*protocol.SeedSince, error) {
	if mark.expired(after) {
		return &protocol.SeedSince{After: int(after), Entries: []protocol.SeedNote{}, Expired: d.expiredSince(mark)}, nil
	}
	entries, err := d.readEntriesSince(read, crownID, after, mark)
	if err != nil {
		return nil, err
	}
	return &protocol.SeedSince{After: int(after), Entries: entries}, nil
}

func (d *Daemon) readEntriesSince(read gardenRead, crownID string, after int64, mark gardenBookmark) ([]protocol.SeedNote, error) {
	tree := read.plots.InPlot(crownID)
	subjects := make([]string, 0, len(tree))
	for _, seed := range tree {
		subjects = append(subjects, seed.ID)
	}
	added, err := d.store.BusEventsAbout(seedEvents.NameNoteAdded, subjects, after, mark.head)
	if err != nil {
		return nil, fmt.Errorf("read the entries saved after %d: %w", after, err)
	}
	schema, err := d.notesCollection()
	if err != nil {
		return nil, err
	}
	entries := make([]protocol.SeedNote, 0, len(added))
	for i := len(added) - 1; i >= 0; i-- {
		event := added[i]
		var payload seedEvents.NoteAddedPayload
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			return nil, fmt.Errorf("save %d names no entry: %w", event.Seq, err)
		}
		doc, found, err := d.store.GetDocument(*schema, payload.NoteID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("save %d names entry %s, which is not in the log", event.Seq, payload.NoteID)
		}
		note, err := garden.DecodeNote(doc.Body)
		if err != nil {
			return nil, fmt.Errorf("entry %s has an unreadable body: %w", payload.NoteID, err)
		}
		entry := noteToProtocol(note, *doc)
		entry.Seq = protocol.Ptr(int(event.Seq))
		entries = append(entries, entry)
	}
	return entries, nil
}

func entriesOfKind(entries []protocol.SeedNote, kind string) []protocol.SeedNote {
	if kind == "" {
		return entries
	}
	out := make([]protocol.SeedNote, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind == kind {
			out = append(out, entry)
		}
	}
	return out
}
