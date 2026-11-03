package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAPlainShellAssignsSeedClaimsToMembers(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	writeCharter(t, s, "keel")
	s.Start()
	var planted protocol.Seed
	s.Attn("seed", "plant", "Assign this seed", "--json").JSON(t, &planted)
	requireStdout(t, s.Attn("seed", "tend", planted.ID, "--for", "Keel"), "tended by Keel")
	requireStdout(t, s.Attn("seed", "show", planted.ID), "tender", "Keel")
}

func TestAnOutpostSessionHoldsItsClaimWhileItsAgentRuns(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app, cli := pair.App, home.Client()
	id := home.Spawn(app, fakeagent.Claude, outpost.Path("remote"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	remote := outpost.LaunchedCarrying(app.Terminal(id))
	session := protocol.SessionID(id)
	planted, err := cli.SeedPlant(session, "Remote work", "Work on the outpost.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seed := planted.Seed.ID
	if _, err := cli.SeedTransition(session, seed, "tend", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	if shown := showSeed(t, home, "", seed).Seed; !shown.Claimed || shown.Ready || protocol.Deref(shown.Tender.SessionID) != session {
		t.Fatalf("while the remote agent runs the seed shows claimed=%v ready=%v tender %+v, want it held by %s", shown.Claimed, shown.Ready, shown.Tender, id)
	}

	remote.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(m protocol.SessionExitedMessage) bool { return m.SessionID == session && m.ExitCode == 1 })
	if shown := showSeed(t, home, "", seed).Seed; shown.Claimed || !shown.Ready || shown.Tender == nil || shown.Tender.SessionID != nil || shown.Tender.Name != "remote" {
		t.Fatalf("after the remote agent exited the seed shows claimed=%v ready=%v tender %+v, want it released and ready while the session stays open", shown.Claimed, shown.Ready, shown.Tender)
	}
}
