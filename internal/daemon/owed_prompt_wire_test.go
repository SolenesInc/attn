package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/testworld"

	"github.com/google/uuid"

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
			w.Launched(session).Exit(0)
			testworld.AwaitSession(app, session, func(s protocol.Session) bool {
				return protocol.Deref(s.StateReason) == "process_exited"
			})
			app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: session})
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return !protocol.Deref(s.TurnOwed) })
			respawnedAt := len(sessionUpdatesOf(app, session))

			w.Spawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
				m.ID = session
				m.InitialPrompt = protocol.Ptr("list the checkout tests")
			})
			run := w.Launched(session)
			workUntilTheVerdict(t, app, session, run, "list the checkout tests", protocol.SessionStateIdle, respawnedAt)
		})
	}
}

func TestInputPlacedWhileTheAgentBootsOpensItsTurnOnlyAtTheVerdict(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := uuid.NewString()
	boot := w.HoldBoot(session)
	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ID = session })
	app.AwaitScreen(session, "? for shortcuts")
	annotate(app, session, "the cart total is off by one")

	boot()
	run := w.Launched(session)
	workUntilTheVerdict(t, app, session, run, "the cart total is off by one", protocol.SessionStateIdle, 0)
}

func TestInputTheUserTypesOverNoLongerHoldsABootingSessionOutOfIdle(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := uuid.NewString()
	boot := w.HoldBoot(session)
	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ID = session })
	app.AwaitScreen(session, "? for shortcuts")
	annotate(app, session, "the cart total is off by one")
	testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: session, Data: "actually", ProbeID: protocol.Ptr("takeover")},
		protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == "takeover" })

	boot()
	w.Launched(session)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "at_prompt"
	})
}

func TestTheNextTurnSettlesOnItsOwnVerdict(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	run := w.Launched(session)
	workUntilTheVerdict(t, app, session, run, "rename the checkout module", protocol.SessionStateWaitingInput, 0)
	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: session})
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return !protocol.Deref(s.TurnOwed) })
	answeredAt := len(sessionUpdatesOf(app, session))

	app.TypeLine(session, "keep the old import path as an alias")
	workUntilTheVerdict(t, app, session, run, "keep the old import path as an alias", protocol.SessionStateIdle, answeredAt)
}

func workUntilTheVerdict(t *testing.T, app *testworld.Peer, session string, run *fakeagent.Run, prompt string, want protocol.SessionState, from int) {
	t.Helper()
	var previous protocol.Session
	var answeredAt time.Time
	if from > 0 {
		previous = sessionUpdatesOf(app, session)[from-1]
		answeredAt = stateSince(t, previous)
	}
	if got := run.Prompted(); got != prompt {
		t.Fatalf("%s took %q, want %q", run.Harness, got, prompt)
	}
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && stateSince(t, s).After(answeredAt)
	})
	run.Reply("Found 14 checkout tests. <!-- attn:state=" + string(want) + " -->")
	verdict := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.State == want && protocol.Deref(s.StateReason) == "classifier_verdict" && stateSince(t, s).After(stateSince(t, working))
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

func annotate(p *testworld.Peer, session, text string) {
	p.T.Helper()
	requestID := uuid.NewString()
	result := testworld.Request(p, protocol.SessionAnnotationsSubmitMessage{
		Cmd:       protocol.CmdSessionAnnotationsSubmit,
		RequestID: requestID,
		SessionID: session,
		Text:      text,
	}, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool {
		return r.RequestID == requestID
	})
	if result.Status != "delivered" {
		p.T.Fatalf("annotation to %s was %s: %s", session, result.Status, protocol.Deref(result.Error))
	}
}

func sessionUpdatesOf(p *testworld.Peer, id string) []protocol.Session {
	var updates []protocol.Session
	for _, e := range p.Received() {
		if e.Session != nil && e.Session.ID == id {
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
