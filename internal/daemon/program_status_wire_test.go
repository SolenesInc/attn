package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClaudeReportingAPermissionPromptPutsTheSessionInApprovalUntilItIsAnswered(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	app.TypeLine(session, "run the migration")
	run.Prompted()
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	run.AskApproval()
	asking := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
	if asking.State != protocol.SessionStatePendingApproval {
		t.Fatalf("while claude reports a permission prompt the session is %s (%s), want pending_approval",
			asking.State, protocol.Deref(asking.StateReason))
	}

	run.Dismiss()
	testworld.AwaitStateAfter(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	run.Reply("Ran it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}
