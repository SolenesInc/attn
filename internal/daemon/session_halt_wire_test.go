package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAHaltedTurnSettlesIdleAndAnOldHaltDoesNotSettleTheNextRun(t *testing.T) {
	for _, agent := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(agent), func(t *testing.T) {
			w := newWorld(t, agent)
			app := w.App()
			cwd := w.Path("shop")
			session := w.Spawn(app, agent, cwd)
			run := w.Launched(session)
			app.TypeLine(session, "write an essay on checkout flows")
			run.Prompted()
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

			run.Halt()
			halted := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
				return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "turn_aborted"
			})

			resumed := respawn(w, app, agent, session, cwd)
			app.TypeLine(session, "just the outline then")
			resumed.Prompted()
			working := testworld.AwaitStateAfter(app, halted, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
			from := indexOfUpdate(sessionUpdatesOf(app, session), working)
			resumed.Reply("Outline ready. Want the intro drafted too? <!-- attn:state=waiting_input -->")
			verdict := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
				return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.StateReason) == "classifier_verdict"
			})

			updates := sessionUpdatesOf(app, session)
			for _, s := range updates[from:indexOfUpdate(updates, verdict)] {
				if s.State != protocol.SessionStateWorking {
					t.Fatalf("the halt from the previous run settled the new turn: %s", describeUpdates(updates[from:]))
				}
			}
		})
	}
}
