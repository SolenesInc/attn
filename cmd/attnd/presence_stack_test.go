package main_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestPresenceNamesEverySeedASessionWorksOn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	seedsOf := func(session string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: []string{"presence", "--seed-ids"}, Session: session})
	}

	cwd := s.Path("api")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	delegated, err := s.Client().Delegate(protocol.DelegateMessage{
		Cmd: protocol.CmdDelegate, RequestID: "presence-seed", Cwd: cwd, Agent: protocol.Ptr("claude"),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr("Sweep the stale branches.")},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Launched(string(delegated.SessionID)).Prompted()
	if got := seedsOf(string(delegated.SessionID)); got.Code != 0 || got.Stdout != delegated.SeedID+"\n" {
		t.Errorf("a delegate's seed: %+v, want %s", got, delegated.SeedID)
	}
	picked := plant(t, s, "Prune the merged worktrees")
	if tend := s.Run(testworld.Invocation{Args: []string{"seed", "tend", picked.ID}, Session: string(delegated.SessionID)}); tend.Code != 0 {
		t.Fatalf("tend: %+v", tend)
	}
	if got := seedsOf(string(delegated.SessionID)); got.Code != 0 || got.Stdout != delegated.SeedID+"\n"+picked.ID+"\n" {
		t.Errorf("a delegate that also tends another seed: %+v, want %s then %s", got, delegated.SeedID, picked.ID)
	}

	const desk = "desk-5555-6666"
	register(t, s, desk, "desk")
	if got := seedsOf(desk); got.Code != 1 || got.Stdout != "" {
		t.Errorf("a session that tends nothing: %+v, want exit 1 and no output", got)
	}
	first := plant(t, s, "Rename the queue band")
	second := plant(t, s, "Trim the palette")
	for _, seed := range []protocol.Seed{first, second} {
		if tend := s.Run(testworld.Invocation{Args: []string{"seed", "tend", seed.ID}, Session: desk}); tend.Code != 0 {
			t.Fatalf("tend: %+v", tend)
		}
	}
	got := seedsOf(desk)
	tended := strings.Fields(got.Stdout)
	slices.Sort(tended)
	want := []string{first.ID, second.ID}
	slices.Sort(want)
	if got.Code != 0 || !slices.Equal(tended, want) {
		t.Errorf("a session that tends two seeds: %+v, want both %v", got, want)
	}
}

func TestAnOutpostSessionKnowsItselfWhileTheHomeIsDown(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	id := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("work"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	terminal := pair.App.Terminal(id)
	outpost.LaunchedCarrying(terminal)
	presence := func(flags ...string) testworld.Result {
		t.Helper()
		return outpost.Run(testworld.Invocation{Terminal: terminal, Args: append([]string{"presence"}, flags...)})
	}
	planted, err := home.Client().SeedPlant(protocol.SessionID(id), "Remote work", "Work on the outpost.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tend := outpost.Run(testworld.Invocation{Terminal: terminal, Args: []string{"seed", "tend", planted.ID}}); tend.Code != 0 {
		t.Fatalf("tend: %+v", tend)
	}
	if got := presence("--seed-ids"); got.Code != 0 || got.Stdout != planted.ID+"\n" {
		t.Fatalf("the outpost session's seeds while the home is up: %+v, want %s", got, planted.ID)
	}

	home.Stop()
	if got := presence(); got.Code != 0 || got.Stdout != "running inside attn (session "+shortID(id)+")\n" {
		t.Errorf("presence while the home is down: %+v, want session %s", got, id)
	}
	if got := presence("--session-id"); got.Code != 0 || got.Stdout != id+"\n" {
		t.Errorf("presence --session-id while the home is down: %+v, want %s", got, id)
	}
	if got := presence("--seed-ids"); got.Code != 1 || got.Stdout != "" || !strings.Contains(got.Stderr, "unreachable") {
		t.Errorf("presence --seed-ids while the home is down: %+v, want exit 1 naming the unreachable home", got)
	}
}
