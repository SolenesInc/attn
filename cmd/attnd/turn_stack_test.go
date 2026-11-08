package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentOpensAndSettlesItsOwnTurnFromItsSession(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(session)
	testworld.AwaitSession(app, session, func(sess protocol.Session) bool { return sess.State == protocol.SessionStateIdle })
	turn := func(action string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: []string{"turn", action}, Session: session})
	}

	for _, step := range []struct{ action, want string }{
		{"open", "Your turn is open; it shows in the user's queue and notifies their phone, if they use them.\n"},
		{"settle", "Your turn is settled.\n"},
		{"settle", "No turn was open; nothing changed.\n"},
	} {
		if got := turn(step.action); got.Code != 0 || got.Stdout != step.want {
			t.Errorf("attn turn %s exited %d and printed %q, stderr %q; want %q", step.action, got.Code, got.Stdout, got.Stderr, step.want)
		}
	}
	if got := s.Run(testworld.Invocation{Args: []string{"turn"}, Session: session}); got.Code != 2 || !strings.Contains(got.Stderr, "usage: attn turn open") {
		t.Errorf("attn turn without a command exited %d with stderr %q, want the usage", got.Code, got.Stderr)
	}
	if help := s.Attn("turn", "--help"); help.Code != 0 || !strings.Contains(help.Stdout, "attn turn settle") {
		t.Errorf("attn turn --help exited %d and printed %q", help.Code, help.Stdout)
	}
}
