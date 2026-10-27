package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASnoozeStillWakesAfterTakingOverALiveConversation(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		owner := w.bubbleClaude(t, app, "shop")
		takingOver := w.bubbleClaude(t, app, "shop")
		session := takingOver.id
		terminal := protocol.TerminalID(takingOver.self)
		if err := cli.UpdateState(terminal, protocol.StateWaitingInput); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
		deadline := time.Now().Add(time.Minute)
		snoozeUntil(app, session, deadline)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return snoozedUntil(s).Equal(deadline) })
		if err := cli.ObserveAgentConversation(terminal, owner.transcript.ConversationID, owner.transcript.Path); err != nil {
			t.Fatal(err)
		}
		if err := cli.UpdateState(terminal, protocol.StateWaitingInput); err != nil {
			t.Fatal(err)
		}
		if got := queriedSession(t, cli, session); !snoozedUntil(got).Equal(deadline) {
			t.Fatalf("takeover lost snooze: %+v", got)
		}
		w.advance(time.Until(deadline))
		got := queriedSession(t, cli, session)
		if !snoozedUntil(got).IsZero() || !protocol.Deref(got.TurnOwed) || !openedAt(got).Equal(deadline) {
			t.Fatalf("takeover at deadline: snoozed=%v owed=%v opened=%v, want awake at %v", snoozedUntil(got), protocol.Deref(got.TurnOwed), openedAt(got), deadline)
		}
	})
}

func TestASnoozeExpiresWhileTheDaemonIsStopped(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
		terminal := protocol.TerminalID(w.Terminal(session))
		if err := cli.UpdateState(terminal, protocol.StateWaitingInput); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
		deadline := time.Now().Add(time.Minute)
		snoozeUntil(app, session, deadline)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return snoozedUntil(s).Equal(deadline) })
		w.stop()
		w.advance(time.Until(deadline))
		w.start()
		got := queriedSession(t, w.Client(), session)
		if !snoozedUntil(got).IsZero() || !protocol.Deref(got.TurnOwed) || !openedAt(got).Equal(deadline) {
			t.Fatalf("restored expired snooze: snoozed=%v owed=%v opened=%v, want awake at %v", snoozedUntil(got), protocol.Deref(got.TurnOwed), openedAt(got), deadline)
		}
	})
}
