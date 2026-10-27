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

	if got := turn("open"); got.Code != 0 || got.Stdout != "The user does not work in the queue flow, so there is no queue to join; nothing changed.\n" {
		t.Errorf("attn turn open with the queue flow off exited %d and printed %q, stderr %q", got.Code, got.Stdout, got.Stderr)
	}
	setSetting(t, app, "queue_mode_enabled", "true")
	for _, step := range []struct{ action, want string }{
		{"open", "Your turn is open; the user will come to you.\n"},
		{"settle", "Your turn is settled; you are out of the user's queue.\n"},
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
