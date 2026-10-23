package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/testworld"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestASessionSpawnedWithAPromptOpensItsTurnOnlyAtTheVerdict(t *testing.T) {
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		for _, verdict := range []protocol.SessionState{protocol.SessionStateIdle, protocol.SessionStateWaitingInput} {
			t.Run(string(h)+"/"+string(verdict), func(t *testing.T) {
				w := newWorld(t, h)
				app := w.App()
				session := w.Spawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
					m.InitialPrompt = protocol.Ptr("list the checkout tests")
				})
				run := w.Launched(session)
				workUntilTheVerdict(t, app, session, run, "list the checkout tests", verdict, 0)
			})
		}
	}
}

func TestASessionRespawnedWithAPromptOpensItsTurnOnlyAtTheVerdict(t *testing.T) {
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		t.Run(string(h), func(t *testing.T) {
			w := newWorld(t, h)
			app := w.App()
			session := w.Spawn(app, h, w.Path("shop"))
			w.Launched(session).Exit(143)
			testworld.AwaitSession(app, session, func(s protocol.Session) bool {
				return protocol.Deref(s.StateReason) == "process_exited"
			})
			app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(session)})
			testworld.AwaitSession(app, session, func(s protocol.Session) bool {
				return s.TerminalExit != nil && protocol.Deref(s.StateReason) == "process_exited" && !protocol.Deref(s.TurnOwed)
			})
			respawnedAt := len(sessionUpdatesOf(app, session))

			w.Spawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
				m.ID = protocol.SessionID(session)
				m.InitialPrompt = protocol.Ptr("list the checkout tests")
			})
			run := w.Launched(session)
			workUntilTheVerdict(t, app, session, run, "list the checkout tests", protocol.SessionStateIdle, respawnedAt)
		})
	}
}

func TestTheNextTurnSettlesOnItsOwnVerdict(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	run := w.Launched(session)
	workUntilTheVerdict(t, app, session, run, "rename the checkout module", protocol.SessionStateWaitingInput, 0)
	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: protocol.SessionID(session)})
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return !protocol.Deref(s.TurnOwed) })
	answeredAt := len(sessionUpdatesOf(app, session))

	app.TypeLine(session, "keep the old import path as an alias")
	workUntilTheVerdict(t, app, session, run, "keep the old import path as an alias", protocol.SessionStateIdle, answeredAt)
}

func workUntilTheVerdict(t *testing.T, app *testworld.Peer, session string, run *fakeagent.Run, prompt string, want protocol.SessionState, from int) {
	t.Helper()
	if got := run.Prompted(); got != prompt {
		t.Fatalf("%s took %q, want %q", run.Harness, got, prompt)
	}
	isWorking := func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking }
	var previous, working protocol.Session
	if from > 0 {
		previous = sessionUpdatesOf(app, session)[from-1]
		working = testworld.AwaitStateAfter(app, previous, isWorking)
	} else {
		working = testworld.AwaitSession(app, session, isWorking)
	}
	run.Reply("Found 14 checkout tests. <!-- attn:state=" + string(want) + " -->")
	verdict := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool {
		return s.State == want && protocol.Deref(s.StateReason) == "classifier_verdict"
	})

	updates := sessionUpdatesOf(app, session)
	for _, s := range updates[from:indexOfUpdate(updates, verdict)] {
		resting := s.State == protocol.SessionStateIdle || s.State == protocol.SessionStateWaitingInput
		entered := s.State != previous.State || s.StateSince != previous.StateSince
		if (resting && entered) || protocol.Deref(s.TurnOwed) {
			t.Fatalf("before its verdict the app saw the session %s (%s) with turn owed %v: %s",
				s.State, protocol.Deref(s.StateReason), protocol.Deref(s.TurnOwed), describeUpdates(updates[from:]))
		}
		previous = s
	}
	opened := turnOpenedAt(t, verdict)
	if !opened.After(stateSince(t, working)) || opened.Before(stateSince(t, verdict)) {
		t.Errorf("the turn opened at %s, want at the verdict (%s), after the working phase began (%s)",
			opened, stateSince(t, verdict), stateSince(t, working))
	}
}

func sessionUpdatesOf(p *testworld.Peer, id string) []protocol.Session {
	var updates []protocol.Session
	for _, e := range p.Received() {
		if e.Session != nil && string(e.Session.ID) == id {
			updates = append(updates, *e.Session)
		}
	}
	return updates
}

func indexOfUpdate(updates []protocol.Session, want protocol.Session) int {
	for i, s := range updates {
		if s.State == want.State && s.StateSince == want.StateSince && protocol.Deref(s.StateReason) == protocol.Deref(want.StateReason) &&
			protocol.Deref(s.TurnOwed) == protocol.Deref(want.TurnOwed) {
			return i
		}
	}
	return len(updates)
}

func describeUpdates(updates []protocol.Session) string {
	described := ""
	for _, s := range updates {
		described += "\n  " + string(s.State) + " (" + protocol.Deref(s.StateReason) + ")"
		if protocol.Deref(s.TurnOwed) {
			described += " turn owed since " + protocol.Deref(s.TurnOpenedAt)
		}
	}
	return described
}

func turnOpenedAt(t *testing.T, s protocol.Session) time.Time {
	t.Helper()
	opened, err := time.Parse(time.RFC3339Nano, protocol.Deref(s.TurnOpenedAt))
	if err != nil {
		t.Fatalf("turn_opened_at %q: %v", protocol.Deref(s.TurnOpenedAt), err)
	}
	return opened
}
