package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASnoozeHoldsTheTurnUntilItsDeadline(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "waiting", "malformed", "woken")
		for _, id := range []string{"waiting", "malformed"} {
			if asked := snoozeReport(t, app, cli, id, protocol.StateWaitingInput); !protocol.Deref(asked.TurnOwed) {
				t.Fatalf("%s is waiting for the user and owes no turn", id)
			}
		}
		snoozeReport(t, app, cli, "woken", protocol.StateWorking)

		app.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: "malformed", Until: "next tuesday"})
		snoozeUntil(app, "waiting", time.Now().Add(time.Hour))
		snoozeUntil(app, "woken", time.Now().Add(time.Minute))
		for _, id := range []string{"waiting", "woken"} {
			held := testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnSnoozedUntil) != "" })
			if protocol.Deref(held.TurnOwed) {
				t.Errorf("%s still owes a turn once snoozed", id)
			}
		}
		if malformed := queriedSession(t, cli, "malformed"); !protocol.Deref(malformed.TurnOwed) || protocol.Deref(malformed.TurnSnoozedUntil) != "" {
			t.Errorf("a snooze until next tuesday left the session owing %v, snoozed until %q; want it still owing and unsnoozed",
				protocol.Deref(malformed.TurnOwed), protocol.Deref(malformed.TurnSnoozedUntil))
		}

		for _, state := range []string{protocol.StateWorking, protocol.StateWaitingInput, protocol.StatePendingApproval, protocol.StateWorking, protocol.StateWaitingInput} {
			if moved := snoozeReport(t, app, cli, "waiting", state); protocol.Deref(moved.TurnOwed) {
				t.Errorf("reporting %s opened a turn on a snoozed session", state)
			}
		}

		w.advance(time.Minute)
		if woken := testworld.AwaitSession(app, "woken", func(s protocol.Session) bool { return protocol.Deref(s.TurnSnoozedUntil) == "" }); protocol.Deref(woken.TurnOwed) {
			t.Error("the snooze woke owing a turn while its agent was still working")
		}
		if due := snoozeReport(t, app, cli, "woken", protocol.StateWaitingInput); !protocol.Deref(due.TurnOwed) {
			t.Error("the agent asked for input after its snooze lapsed and owes no turn")
		}
	})
}

func TestStuckBreaksThroughASnoozeButAQuestionDoesNot(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		registerSessions(t, w, cli, "stuck", "asking")
		for _, id := range []string{"stuck", "asking"} {
			snoozeReport(t, app, cli, id, protocol.StateWorking)
			snoozeUntil(app, id, time.Now().Add(time.Hour))
			testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnSnoozedUntil) != "" })
		}
		snoozeReport(t, app, cli, "asking", protocol.StateWaitingInput)

		w.advance(90*time.Second + time.Millisecond)
		broke := testworld.AwaitSession(app, "stuck", func(s protocol.Session) bool { return s.State == protocol.SessionStateUnknown })
		if !protocol.Deref(broke.TurnOwed) || protocol.Deref(broke.TurnSnoozedUntil) != "" {
			t.Errorf("the stuck session owes %v, snoozed until %q; want the snooze ended and the turn open",
				protocol.Deref(broke.TurnOwed), protocol.Deref(broke.TurnSnoozedUntil))
		}
		if held := queriedSession(t, cli, "asking"); protocol.Deref(held.TurnOwed) || protocol.Deref(held.TurnSnoozedUntil) == "" {
			t.Errorf("the session asking a question owes %v, snoozed until %q; want it still snoozed",
				protocol.Deref(held.TurnOwed), protocol.Deref(held.TurnSnoozedUntil))
		}
	})
}

func snoozeReport(t *testing.T, app *testworld.Peer, cli *client.Client, id, state string) protocol.Session {
	t.Helper()
	report := cli.UpdateState
	if state == protocol.StatePendingApproval {
		report = func(id, _ string) error { return cli.RecordNotification(id, "permission_prompt", "Allow edit?") }
	}
	if err := report(id, state); err != nil {
		t.Fatalf("%s reports %s: %v", id, state, err)
	}
	return testworld.AwaitSession(app, id, func(s protocol.Session) bool { return string(s.State) == state })
}
