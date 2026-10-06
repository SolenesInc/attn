package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestALivePiAgentWaitingForTheUserStillWaitsAfterADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Pi))
	s.Start()
	app := s.App()
	awaitAgentAvailable(app, fakeagent.Pi)
	session := s.Spawn(app, fakeagent.Pi, s.Path("shop"))
	pi := s.Launched(session)
	app.TypeLine(session, "price the checkout")
	pi.Prompted()
	working := testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	pi.Reply("Which currency? <!-- attn:state=waiting_input -->")
	testworld.AwaitStateAfter(app, working, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })

	s.Stop()
	s.Start()
	app = s.App()
	for _, x := range app.Initial.Sessions {
		if string(x.ID) != session {
			continue
		}
		if x.State != protocol.SessionStateWaitingInput {
			t.Fatalf("the pi agent waiting for the user came back %s (%s), want waiting_input", x.State, protocol.Deref(x.StateReason))
		}
		return
	}
	t.Fatalf("the live pi session %s is missing after the restart", session)
}
