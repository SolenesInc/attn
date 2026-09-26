package daemon

import (
	"encoding/json"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
)

func gardenCall(t *testing.T, run func(net.Conn)) protocol.Response {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		run(server)
		_ = server.Close()
	}()
	defer client.Close()
	var resp protocol.Response
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func newGardenDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "sess-a", Label: "a",
		State: "idle", StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.register("ws-1", "a", "/tmp/a", "a0", false, false)
	d.workspaces.associateSession("sess-a", "ws-1", "a")
	return d
}

func plant(t *testing.T, d *Daemon, msg protocol.SeedPlantMessage) protocol.Seed {
	t.Helper()
	msg.Cmd = protocol.CmdSeedPlant
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedPlant(c, &msg) })
	if !resp.Ok {
		t.Fatalf("plant %q: %v", msg.Title, protocol.Deref(resp.Error))
	}
	return resp.SeedPlantResult.Seed
}

func editSeed(t *testing.T, d *Daemon, seedID, body string) protocol.Seed {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedEdit(c, &protocol.SeedEditMessage{Cmd: protocol.CmdSeedEdit, SeedID: seedID, Body: body})
	})
	if !resp.Ok {
		t.Fatalf("edit %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedEditResult.Seed
}

func addGardenSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, State: "idle",
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.associateSession(id, "ws-1", id)
}

func move(t *testing.T, d *Daemon, session, seedID string, verb garden.Verb, reason, member string) protocol.Seed {
	t.Helper()
	resp := transition(t, d, session, seedID, verb, reason, member)
	if !resp.Ok {
		t.Fatalf("%s %s: %v", verb, seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedTransitionResult.Seed
}

func transition(t *testing.T, d *Daemon, session, seedID string, verb garden.Verb, reason, member string) protocol.Response {
	t.Helper()
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seedID, Verb: string(verb),
	}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if reason != "" {
		msg.Reason = protocol.Ptr(reason)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	return gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
}

func note(t *testing.T, d *Daemon, session, seedID, body, member string) protocol.SeedNote {
	t.Helper()
	msg := protocol.SeedNoteMessage{Cmd: protocol.CmdSeedNote, SeedID: seedID, Body: body}
	if session != "" {
		msg.SourceSessionID = protocol.Ptr(session)
	}
	if member != "" {
		msg.Member = protocol.Ptr(member)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedNote(c, &msg) })
	if !resp.Ok {
		t.Fatalf("note on %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedNoteResult.Note
}

func show(t *testing.T, d *Daemon, seedID string) *protocol.SeedShowResult {
	t.Helper()
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedShow(c, &protocol.SeedShowMessage{Cmd: protocol.CmdSeedShow, SeedID: seedID})
	})
	if !resp.Ok {
		t.Fatalf("show %s: %v", seedID, protocol.Deref(resp.Error))
	}
	return resp.SeedShowResult
}

func TestGarden_EveryMovePublishesItsOwnFact(t *testing.T) {
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

	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")
	editSeed(t, d, seed.ID, "edited while tended")
	note(t, d, "sess-a", seed.ID, "on the log", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "done", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbReplant, "", "trellis")
	move(t, d, "sess-a", seed.ID, garden.VerbWither, "", "trellis")

	want := []string{
		seedEvents.NameTended, seedEvents.NameBodyEdited, seedEvents.NameNoteAdded, seedEvents.NameParked,
		seedEvents.NameHarvested, seedEvents.NameReplanted, seedEvents.NameWithered,
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("the bus saw %v, want %v", seen, want)
	}
}

func TestGarden_EditRejectsUnknownSeedAndOversizedBody(t *testing.T) {
	d := newGardenDaemon(t)
	unknown := gardenCall(t, func(c net.Conn) {
		d.handleSeedEdit(c, &protocol.SeedEditMessage{Cmd: protocol.CmdSeedEdit, SeedID: "s-ffffff", Body: "words"})
	})
	if unknown.Ok || !strings.Contains(protocol.Deref(unknown.Error), "s-ffffff") {
		t.Fatalf("unknown edit = %+v, want named seed refusal", unknown)
	}
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Bounded"})
	oversized := gardenCall(t, func(c net.Conn) {
		d.handleSeedEdit(c, &protocol.SeedEditMessage{
			Cmd: protocol.CmdSeedEdit, SeedID: seed.ID, Body: strings.Repeat("x", garden.MaxBodyBytes+1),
		})
	})
	if oversized.Ok || !strings.Contains(protocol.Deref(oversized.Error), "max_body_bytes") {
		t.Fatalf("oversized edit = %+v, want named body limit", oversized)
	}
}

func TestGarden_PlantingMintsAgainWhenAnIDIsTaken(t *testing.T) {
	d := newGardenDaemon(t)
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	if _, err := d.plantSeed(*schema, garden.Seed{ID: "s-7k3f9m", Title: "already here", Status: garden.StatusPlanted}); err != nil {
		t.Fatalf("seeding the collision: %v", err)
	}
	minted := []string{"s-7k3f9m", "s-7k3f9m", "s-fresh1"}
	d.gardenMintID = func() (string, error) {
		next := minted[0]
		minted = minted[1:]
		return next, nil
	}

	planted := plant(t, d, protocol.SeedPlantMessage{Title: "planted anyway"})
	if planted.ID != "s-fresh1" {
		t.Fatalf("seed id = %q, want the third mint after two taken ones", planted.ID)
	}
	if len(minted) != 0 {
		t.Fatalf("%d mints unused: the retry stopped early", len(minted))
	}

	d.gardenMintID = func() (string, error) { return "s-7k3f9m", nil }
	resp := gardenCall(t, func(c net.Conn) {
		d.handleSeedPlant(c, &protocol.SeedPlantMessage{Cmd: protocol.CmdSeedPlant, Title: "no id left"})
	})
	if resp.Ok {
		t.Fatal("a mint source that never moves was allowed to plant")
	}
	if msg := protocol.Deref(resp.Error); !strings.Contains(msg, "random source") {
		t.Fatalf("refusal = %q, want it to name the mint source", msg)
	}
}

func TestSeedParkRollsBackWhenItsCommentCannotLand(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "work to keep claimed"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")

	d.gardenMintNoteID = func() (string, error) { return "n-000000", nil }
	note(t, d, "sess-a", seed.ID, "this id is already taken", "")
	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(garden.VerbPark),
		SourceSessionID: protocol.Ptr("sess-a"), Comment: protocol.Ptr("must land atomically"),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "every one was taken") {
		t.Fatalf("park response = %+v", resp)
	}
	got, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != garden.StatusGrowing || got.TenderSession != "sess-a" {
		t.Fatalf("seed moved without its comment: %+v", got)
	}
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Body != "this id is already taken" {
		t.Fatalf("failed Park left a partial comment: %+v", notes)
	}
}

func TestForcedSeedMoveRollsBackWhenItsAuditNoteCannotLand(t *testing.T) {
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-a")
	addGardenSession(t, d, "sess-b")
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "held work"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")

	d.gardenMintNoteID = func() (string, error) { return "n-000000", nil }
	note(t, d, "sess-a", seed.ID, "the note that already owns this id", "")
	beforeFacts, err := d.store.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("read facts before forced move: %v", err)
	}

	msg := protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, SeedID: seed.ID, Verb: string(garden.VerbWither),
		SourceSessionID: protocol.Ptr("sess-b"), Force: protocol.Ptr(true),
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedTransition(c, &msg) })
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "every one was taken") {
		t.Fatalf("forced move response = %+v", resp)
	}

	got, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatalf("read seed after refused batch: %v", err)
	}
	if got.Status != garden.StatusGrowing || got.TenderSession != "sess-a" {
		t.Fatalf("seed moved without its audit: %+v", got)
	}
	notes, _, err := d.readNotes(seed.ID, 10)
	if err != nil {
		t.Fatalf("read notes after refused batch: %v", err)
	}
	if len(notes) != 1 || notes[0].Body != "the note that already owns this id" {
		t.Fatalf("failed batch left a false audit: %+v", notes)
	}
	afterFacts, err := d.store.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("read facts after forced move: %v", err)
	}
	if len(afterFacts) != len(beforeFacts) {
		t.Fatalf("failed batch announced facts: before=%d after=%d", len(beforeFacts), len(afterFacts))
	}
}
