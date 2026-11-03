package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestALivePluginAgentWaitingForTheUserStillWaitsAfterADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Plug))
	s.Start()
	app := s.App()
	awaitAgentAvailable(app, fakeagent.Plug)
	session := s.Spawn(app, fakeagent.Plug, s.Path("shop"))
	plug := s.Launched(session)
	app.TypeLine(session, "price the checkout")
	plug.Prompted()
	working := testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	plug.Reply("Which currency? <!-- attn:state=waiting_input -->")
	testworld.AwaitStateAfter(app, working, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })

	s.Stop()
	s.Start()
	app = s.App()
	for _, x := range app.Initial.Sessions {
		if string(x.ID) != session {
			continue
		}
		if x.State != protocol.SessionStateWaitingInput {
			t.Fatalf("the plug agent waiting for the user came back %s (%s), want waiting_input", x.State, protocol.Deref(x.StateReason))
		}
		return
	}
	t.Fatalf("the live plug session %s is missing after the restart", session)
}
