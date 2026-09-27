package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACodexApprovalPromptPutsTheSessionInApprovalUntilItIsAnswered(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session, run, working := codexAtWork(t, w, app)

	run.AskApproval()
	asking := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	if asking.State != protocol.SessionStatePendingApproval {
		t.Fatalf("while codex shows its approval prompt the session is %s (%s), want pending_approval",
			asking.State, protocol.Deref(asking.StateReason))
	}

	run.Dismiss()
	testworld.AwaitStateAfter(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("Ran it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestCodexsHooksNamingTheDefaultModeDoNotRetireTheReviewerItWasSpawnedWith(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "auto_approve_enabled", "true")
	session, run, _ := codexAtWork(t, w, app)

	run.AskApproval()
	run.Dismiss()
	run.Reply("Ran it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	for _, s := range sessionUpdatesOf(app, session) {
		if s.State == protocol.SessionStatePendingApproval {
			t.Fatalf("the app saw an approval the reviewer answered inside its dwell: %s", describeUpdates(sessionUpdatesOf(app, session)))
		}
	}
}

func codexAtWork(t *testing.T, w *world, app *testworld.Peer) (string, *fakeagent.Run, protocol.Session) {
	t.Helper()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	run := w.Launched(session)
	app.TypeLine(session, "run the migration")
	run.Prompted()
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	return session, run, working
}
