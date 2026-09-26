package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWakeOpensTheTurnAtTheWakeInstant(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
			t.Fatalf("report waiting_input: %v", err)
		}
		owed := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

		until := time.Now().Add(time.Minute)
		snoozeUntil(app, "s1", until)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnSnoozedUntil) != "" })
		if err := cli.UpdateState("s1", protocol.StateIdle); err != nil {
			t.Fatalf("report idle: %v", err)
		}
		w.advance(time.Minute)

		woken := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return openedAt(s).Equal(until) })
		if protocol.Deref(woken.TurnOpenedAt) == protocol.Deref(owed.TurnOpenedAt) {
			t.Error("the turn kept its pre-snooze age, so it wakes to the head of the queue")
		}
		if !protocol.Deref(woken.TurnOwed) {
			t.Error("the woken session owes no turn although it is sitting idle")
		}
		if snoozed := protocol.Deref(woken.TurnSnoozedUntil); snoozed != "" {
			t.Errorf("the deadline %s survived the wake", snoozed)
		}
	})
}

func openedAt(s protocol.Session) time.Time {
	opened, _ := time.Parse(time.RFC3339Nano, protocol.Deref(s.TurnOpenedAt))
	return opened
}

func TestAReplacedSnoozeWakesOnlyAtItsOwnDeadline(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		registerSessions(t, w, cli, "s1")
		if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
			t.Fatalf("report waiting_input: %v", err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

		first := time.Now().Add(time.Minute)
		replacement := time.Now().Add(2 * time.Minute)
		snoozeUntil(app, "s1", first)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return snoozedUntil(s).Equal(first) })
		snoozeUntil(app, "s1", replacement)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return snoozedUntil(s).Equal(replacement) })

		w.advance(time.Minute)
		held := queriedSession(t, cli, "s1")
		if !snoozedUntil(held).Equal(replacement) || protocol.Deref(held.TurnOwed) {
			t.Errorf("after the replaced deadline passed: snoozed until %q, owed %v; want still snoozed and owing nothing",
				protocol.Deref(held.TurnSnoozedUntil), protocol.Deref(held.TurnOwed))
		}

		w.advance(time.Until(replacement))
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return openedAt(s).Equal(replacement) && protocol.Deref(s.TurnOwed) && protocol.Deref(s.TurnSnoozedUntil) == ""
		})
	})
}

func TestSnoozingABusySessionKeepsTheDeadline(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	app.TypeLine(session, "run the migration")
	run.Prompted()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	until := time.Now().Add(time.Hour)
	snoozeUntil(app, session, until)
	snoozed := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return snoozedUntil(s).Equal(until) })

	run.Reply("Migrated. <!-- attn:state=idle -->")
	finished := testworld.AwaitStateAfter(app, snoozed, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if protocol.Deref(finished.TurnOwed) || !snoozedUntil(finished).Equal(until) {
		t.Errorf("the finished run owes %v, snoozed until %q; want no turn and the snooze kept until %s",
			protocol.Deref(finished.TurnOwed), protocol.Deref(finished.TurnSnoozedUntil), until)
	}
}

func TestAPendingSnoozeOutlivesARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	run := w.Launched(session)
	app.TypeLine(session, "add a discount field")
	run.Prompted()
	run.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

	until := time.Now().Add(time.Hour).Truncate(time.Second)
	snoozeUntil(app, session, until)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return snoozedUntil(s).Equal(until) })

	w.restart()
	app = w.App()
	i := slices.IndexFunc(app.Initial.Sessions, func(s protocol.Session) bool { return s.ID == session })
	if i < 0 {
		t.Fatal("the snoozed session did not come back after a restart")
	}
	if got := app.Initial.Sessions[i]; !snoozedUntil(got).Equal(until) || protocol.Deref(got.TurnOwed) {
		t.Errorf("after a restart the session is snoozed until %q and owes %v, want %s and nothing owed",
			protocol.Deref(got.TurnSnoozedUntil), protocol.Deref(got.TurnOwed), until)
	}
}

func snoozedUntil(s protocol.Session) time.Time {
	until, _ := time.Parse(time.RFC3339Nano, protocol.Deref(s.TurnSnoozedUntil))
	return until
}
