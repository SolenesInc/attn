package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func moddedClaudeWithVerdicts(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run) {
	t.Helper()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("deploy work")
	})
	run := w.Launched(session)
	w.awaitTimer(daemon.ClaudeModPollHoldTimer)
	return session, run
}

func parkOnBackgroundWork(t *testing.T, w *world, cli *client.Client, run *fakeagent.Run, session string) {
	t.Helper()
	run.Stream("The deploy is running in the background.")
	transcript := fakeagent.WriteClaudeTranscript(t, w.Path("shop"), run.ConversationID)
	if err := cli.SendStop(protocol.TerminalID(w.Terminal(session)), transcript.Path, client.StopFacts{BackgroundTasks: []protocol.StopBackgroundTask{
		{Type: "background_session", Status: "running", Name: protocol.Ptr("./deploy.sh staging")},
	}}); err != nil {
		t.Fatalf("stop with background work: %v", err)
	}
	run.ProgramStatus("state=done:app=claude-code")
	answerTurnVerdict(t, w, "PARKED")
}

func annotationReturnedWhileWorking(t *testing.T, w *world, app *testworld.Peer, run *fakeagent.Run, session string) *testworld.Pause {
	t.Helper()
	workingOn(t, app, run, session, "deploy to staging")
	delivered(t, app, session, "check the rollout")
	modEvent(t, run, fakeagent.ModEventHeld, "check the rollout")
	waits := w.PauseEvery(pausepoint.ClaudeModReturnWaits)
	run.ModReload()
	waits.Await()
	waits.Release()
	return waits
}

func TestAnAnnotationReturnedWhileWorkingReachesTheAgentWhenItParks(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session, run := moddedClaudeWithVerdicts(t, w, app)
	annotationReturnedWhileWorking(t, w, app, run, session)

	parkOnBackgroundWork(t, w, cli, run, session)

	if got := run.Prompted(); got != "check the rollout" {
		t.Fatalf("the parked agent took %q, want the returned annotation", got)
	}
}

func TestAnAnnotationReturnedWhileWorkingWaitsOutAnApprovalThenReachesTheParkedAgent(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "auto_approve_enabled", "false")
	session, run := moddedClaudeWithVerdicts(t, w, app)
	waits := annotationReturnedWhileWorking(t, w, app, run, session)
	terminal := protocol.TerminalID(w.Terminal(session))

	if err := cli.RecordNotification(terminal, "permission_prompt", "Allow edit?"); err != nil {
		t.Fatalf("report the permission prompt: %v", err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	waits.Await()
	waits.Release()

	if err := cli.UpdateStateFromHookEvidence(terminal, protocol.StateWorking, "", "", ""); err != nil {
		t.Fatalf("report the approval answered: %v", err)
	}
	waits.Await()
	waits.Release()

	parkOnBackgroundWork(t, w, cli, run, session)
	if got := run.Prompted(); got != "check the rollout" {
		t.Fatalf("the parked agent took %q, want the returned annotation", got)
	}
}
