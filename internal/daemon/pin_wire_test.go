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

func TestAShellOpenedFromASessionNamesThatSessionAsItsParent(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := w.Path("api")
	agent := w.Spawn(app, fakeagent.Codex, cwd)
	w.Launched(agent)
	shellFrom := func(base string) string {
		return w.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), cwd, func(m *protocol.SpawnSessionMessage) {
			m.SpawnedFrom = protocol.Ptr(base)
		})
	}
	shell := shellFrom(agent)
	nested := shellFrom(shell)
	for _, id := range []string{shell, nested} {
		testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.ParentSessionID) == agent })
	}
	for _, id := range []string{shell, nested} {
		if got := queriedSession(t, cli, id).ParentSessionID; protocol.Deref(got) != agent {
			t.Errorf("queried shell %s names parent %q, want %s", id, protocol.Deref(got), agent)
		}
		app.TypeLine(id, "exit")
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == id })
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
