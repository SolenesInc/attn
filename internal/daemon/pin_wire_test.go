package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestPinningStampsTheInstantAndLeavesTheOwedTurnAlone(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
			t.Fatalf("report waiting_input: %v", err)
		}
		owed := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })

		w.advance(time.Minute)
		firstPin := time.Now()
		pin(app, "s1", true)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return pinnedAt(t, s).Equal(firstPin) })

		w.advance(time.Minute)
		watcher := w.App()
		pin(app, "s1", false)
		unpinned := testworld.AwaitSession(watcher, "s1", func(s protocol.Session) bool { return s.PinnedAt == nil })
		if !protocol.Deref(unpinned.TurnOwed) || protocol.Deref(unpinned.TurnOpenedAt) != protocol.Deref(owed.TurnOpenedAt) {
			t.Errorf("after a pin and unpin the turn is owed=%v opened at %q, want it still owed since %q",
				protocol.Deref(unpinned.TurnOwed), protocol.Deref(unpinned.TurnOpenedAt), protocol.Deref(owed.TurnOpenedAt))
		}

		w.advance(time.Minute)
		repin := time.Now()
		pin(app, "s1", true)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return pinnedAt(t, s).Equal(repin) })
	})
}

func TestPinningTakesOnlyThatSessionOutOfTheQueueWhileItsTurnsKeepOpening(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	cwd := w.Path("shop")
	pinned := w.Spawn(app, fakeagent.Claude, cwd)
	sibling := w.Spawn(app, fakeagent.Claude, cwd)
	run := w.Launched(pinned)
	w.Launched(sibling)
	owedSince := map[string]protocol.Session{}
	for _, id := range []string{pinned, sibling} {
		owedSince[id] = testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	}

	pin(app, pinned, true)
	testworld.AwaitSession(app, pinned, func(s protocol.Session) bool {
		return s.PinnedAt != nil && !protocol.Deref(s.TurnOwed)
	})
	if got := queriedSession(t, cli, sibling); !protocol.Deref(got.TurnOwed) || got.PinnedAt != nil {
		t.Fatalf("after pinning %s its sibling is owed=%v pinned_at=%q, want it still owed and unpinned",
			pinned, protocol.Deref(got.TurnOwed), protocol.Deref(got.PinnedAt))
	}

	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: pinned})
	app.TypeLine(pinned, "run the checkout tests")
	run.Prompted()
	working := testworld.AwaitSession(app, pinned, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && s.PinnedAt != nil
	})
	run.Reply("The unit suite passes. Ship it? <!-- attn:state=waiting_input -->")
	whilePinned := testworld.AwaitSession(app, pinned, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && stateSince(t, s).After(stateSince(t, working))
	})
	if protocol.Deref(whilePinned.TurnOwed) {
		t.Fatal("a turn that opened while the session was pinned put it back in the queue")
	}

	pin(app, pinned, false)
	testworld.AwaitSession(app, pinned, func(s protocol.Session) bool {
		return s.PinnedAt == nil && protocol.Deref(s.TurnOwed) && turnOpenedAt(t, s).After(turnOpenedAt(t, owedSince[pinned]))
	})

	chief := w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	w.Launched(chief)
	for _, id := range []string{chief, "session-nobody-spawned"} {
		pin(app, id, true)
		if refusal := testworld.Refused(app); protocol.Deref(refusal.Cmd) != protocol.CmdPinSession {
			t.Fatalf("pinning %s was refused for %q, want the pin_session refusal", id, protocol.Deref(refusal.Cmd))
		}
	}
	if got := queriedSession(t, cli, chief); got.PinnedAt != nil {
		t.Fatalf("the refused pin of the chief was recorded at %q", protocol.Deref(got.PinnedAt))
	}
}

func TestAPinnedSessionStaysPinnedWhenRespawned(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("api")
	session := w.Spawn(app, fakeagent.Codex, cwd)
	w.Launched(session)
	pin(app, session, true)
	pinned := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.PinnedAt != nil })

	respawn(w, app, fakeagent.Codex, session, cwd)
	if got := queriedSession(t, w.Client(), session).PinnedAt; protocol.Deref(got) != protocol.Deref(pinned.PinnedAt) {
		t.Fatalf("after a respawn pinned_at = %q, want the pin kept at %q", protocol.Deref(got), protocol.Deref(pinned.PinnedAt))
	}
}

func queriedSession(t *testing.T, cli *client.Client, id string) protocol.Session {
	t.Helper()
	listed, err := cli.Query("")
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	for _, s := range listed {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %s is not listed", id)
	return protocol.Session{}
}

func pin(app *testworld.Peer, sessionID string, pinned bool) {
	app.Send(protocol.PinSessionMessage{Cmd: protocol.CmdPinSession, SessionID: sessionID, Pinned: pinned})
}

func pinnedAt(t *testing.T, s protocol.Session) time.Time {
	t.Helper()
	if s.PinnedAt == nil {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, *s.PinnedAt)
	if err != nil {
		t.Fatalf("pinned_at %q is not RFC 3339: %v", *s.PinnedAt, err)
	}
	return at
}
