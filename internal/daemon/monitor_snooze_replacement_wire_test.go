package daemon_test

import (
	"fmt"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
	"os"
	"testing"
	"time"
)

func TestASnoozeStillWakesAfterTakingOverALiveConversation(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		cwd := w.Path("shop")
		var sessions, paths []string
		for _, native := range []string{"live-owner", "taking-over"} {
			session := w.Spawn(app, fakeagent.Codex, cwd)
			path := w.Path(native + ".jsonl")
			if err := os.WriteFile(path, []byte(fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":%q,\"source\":\"cli\"}}\n", native, cwd)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := cli.ObserveAgentConversation(protocol.TerminalID(w.Terminal(session)), native, path); err != nil {
				t.Fatal(err)
			}
			sessions = append(sessions, session)
			paths = append(paths, path)
		}
		session := sessions[1]
		terminal := protocol.TerminalID(w.Terminal(session))
		if err := cli.UpdateState(terminal, protocol.StateWaitingInput); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
		deadline := time.Now().Add(time.Minute)
		snoozeUntil(app, session, deadline)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return snoozedUntil(s).Equal(deadline) })
		if err := cli.ObserveAgentConversation(terminal, "live-owner", paths[0]); err != nil {
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
