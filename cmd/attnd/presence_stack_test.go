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
