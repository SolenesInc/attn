package main_test

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestPresenceNamesTheSeedASessionWorksOn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	seedOf := func(session string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: []string{"presence", "--seed-id"}, Session: session})
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
	if got := seedOf(string(delegated.SessionID)); got.Code != 0 || got.Stdout != delegated.SeedID+"\n" {
		t.Errorf("a delegate's seed: %+v, want %s", got, delegated.SeedID)
	}

	const desk = "desk-5555-6666"
	register(t, s, desk, "desk")
	if got := seedOf(desk); got.Code != 1 || got.Stdout != "" {
		t.Errorf("a session that tends nothing: %+v, want exit 1 and no output", got)
	}
	first := plant(t, s, "Rename the queue band")
	if tend := s.Run(testworld.Invocation{Args: []string{"seed", "tend", first.ID}, Session: desk}); tend.Code != 0 {
		t.Fatalf("tend: %+v", tend)
	}
	if got := seedOf(desk); got.Code != 0 || got.Stdout != first.ID+"\n" {
		t.Errorf("a session that tends one seed: %+v, want %s", got, first.ID)
	}
	second := plant(t, s, "Trim the palette")
	if tend := s.Run(testworld.Invocation{Args: []string{"seed", "tend", second.ID}, Session: desk}); tend.Code != 0 {
		t.Fatalf("tend: %+v", tend)
	}
	if got := seedOf(desk); got.Code != 1 || got.Stdout != "" {
		t.Errorf("a session that tends two seeds: %+v, want exit 1 and no output", got)
	}
}
