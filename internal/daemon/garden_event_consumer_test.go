package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
)

func TestGardenSeedEventForARemoteTenderDoesNotBlockLaterLocalBell(t *testing.T) {
	d := newGardenDaemon(t)
	endpoint, err := d.store.AddEndpoint("gpu-box", "gpu.example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil, nil)
	if !d.hubManager.ReplaceRemoteSessions(endpoint.ID, []protocol.Session{{ID: "remote-worker"}}) {
		t.Fatal("remote session was not registered")
	}

	remoteWire := plant(t, d, protocol.SeedPlantMessage{Title: "Remote work"})
	remote, _, err := d.readSeed(remoteWire.ID)
	if err != nil {
		t.Fatal(err)
	}
	remote.TenderSession = "remote-worker"
	remote.Status = garden.StatusGrowing
	raw, err := remote.Encode()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.PutDocument(*schema, remote.ID, raw, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if err := d.handleSeedEventForTest(
		seedEvents.NameNoteAdded, remote.ID,
		seedEvents.NoteAddedPayload{NoteID: "n-remote", AttentionRequested: true},
	); err != nil {
		t.Fatalf("remote-tender event: %v", err)
	}

	addGardenSession(t, d, "local-watcher")
	local := plant(t, d, protocol.SeedPlantMessage{Title: "Local work"})
	watchSeed(t, d, "local-watcher", local.ID, false)
	if err := d.handleSeedEventForTest(
		seedEvents.NameNoteAdded, local.ID,
		seedEvents.NoteAddedPayload{NoteID: "n-local", AttentionRequested: true},
	); err != nil {
		t.Fatalf("later local event: %v", err)
	}
	if queued := queuedSeedBells(t, d, "remote-worker"); len(queued) != 0 {
		t.Fatalf("home queued a bell for the remote tender: %q", queued)
	}
	assertOneSeedBell(t, d, "local-watcher", local.ID, "note.added")
}
