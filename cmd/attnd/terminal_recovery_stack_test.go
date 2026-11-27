package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARestartKeepsALiveShellAndFinishesAnInterruptedClose(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	live := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("live"))
	app.TypeLine(live, "echo kept-$((1+1))")
	app.AwaitScreen(live, "kept-2")
	closing := s.Spawn(app, fakeagent.Claude, s.Path("closing"))
	closingRun := s.Launched(closing)
	s.Stop()

	s.StartCrashingAt("session-teardown-before-kill")
	app = s.App()
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: protocol.SessionID(closing)})
	s.AwaitCrash()

	s.Start()
	app = s.App()
	closingRun.AwaitGone()
	if x := initialSession(t, app, live); x.State == protocol.SessionStateRecoverable {
		t.Errorf("the live shell came back %s", x.State)
	}
	app.AwaitScreen(live, "kept-2")
	for _, x := range app.Initial.Sessions {
		if string(x.ID) == closing {
			t.Errorf("the session closed before the crash came back in state %s", x.State)
		}
	}
}
