package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestWakeOpensTheTurnAtTheWakeInstant(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.app()
		cli := w.cli()
		if err := cli.Register("s1", "s1", w.path("s1")); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
			t.Fatalf("report waiting_input: %v", err)
		}
		owed := awaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

		until := time.Now().Add(time.Minute)
		app.send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: "s1", Until: until.Format(time.RFC3339Nano)})
		awaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnSnoozedUntil) != "" })
		if err := cli.UpdateState("s1", protocol.StateIdle); err != nil {
			t.Fatalf("report idle: %v", err)
		}
		w.advance(time.Minute)

		woken := awaitSession(app, "s1", func(s protocol.Session) bool { return openedAt(s).Equal(until) })
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
