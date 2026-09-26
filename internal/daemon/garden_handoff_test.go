package daemon

import (
	"net"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
)

func handoff(t *testing.T, d *Daemon, session, seedID, body, member string) protocol.SeedNote {
	t.Helper()
	msg := protocol.SeedNoteMessage{
		Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body, Kind: protocol.Ptr(garden.NoteKindHandoff),
	}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
	if !resp.Ok {
		t.Fatalf("handoff on %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNoteResult.Note
}

func TestGarden_AHandoffPublishesTheNoteFact(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "facts"})

	var seen []string
	unsubscribe := d.eventBus.Subscribe(bus.Filter{"garden.*"}, func(ev bus.Event) {
		if ev.Subject != seed.ID {
			t.Errorf("fact %s names subject %q, want the seed", ev.Name, ev.Subject)
		}
		seen = append(seen, ev.Name)
	})
	defer unsubscribe()

	handoff(t, d, "sess-a", seed.ID, "over to you", "keel")

	if !slices.Equal(seen, []string{seedEvents.NameNoteAdded}) {
		t.Fatalf("a handoff published %v, want just %s", seen, seedEvents.NameNoteAdded)
	}
}
