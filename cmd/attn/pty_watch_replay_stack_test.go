package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnApprovalAskedWhileTheDaemonWasDownStillWaitsAfterTheRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "run the migration")
	codex.Prompted()
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })

	s.Stop()
	codex.AskApproval()
	s.Start()
	app = s.App()
	var came protocol.Session
	for _, x := range app.Initial.Sessions {
		if x.ID == session {
			came = x
		}
	}
	if came.State == protocol.SessionStateWorking || came.State == protocol.SessionStateLaunching || came.State == protocol.SessionStateUnknown {
		came = testworld.AwaitSession(app, session, func(x protocol.Session) bool {
			return x.State != protocol.SessionStateWorking && x.State != protocol.SessionStateLaunching && x.State != protocol.SessionStateUnknown
		})
	}
	if came.State != protocol.SessionStatePendingApproval {
		t.Errorf("the agent asking for approval came back %s (%s), want pending approval", came.State, protocol.Deref(came.StateReason))
	}
}

func TestAnApprovalAskedWhileTheRestartedDaemonStartsWatchingIsNeverLost(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	replay := s.PauseAt(pausepoint.PtyWatchReplay)
	boot := s.HoldNextBoot()
	s.Start()
	app := s.App()
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	terminal := app.Terminal(session)
	s.AwaitHeldBoot()
	s.Stop()
	boot()
	codex := s.LaunchedCarrying(terminal)

	s.Start()
	replay.Await()
	app = s.App()
	codex.AskApproval()
	app.AwaitScreen(session, "Allow the command to run?")
	replay.Release()
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStatePendingApproval })
}
