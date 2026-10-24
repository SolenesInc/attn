package daemon_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/testworld"
)

func waitingAgentStack(t *testing.T) (*testworld.Stack, string) {
	t.Helper()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	agent := s.Launched(id)
	app.TypeLine(id, "pick a colour")
	agent.Prompted()
	agent.Reply("Which colour? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, id, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	return s, id
}

func TestAConversationSwitchSavedJustBeforeACrashIsDecidedAfterTheRestart(t *testing.T) {
	t.Parallel()
	s, id := waitingAgentStack(t)
	terminal := protocol.TerminalID(s.App().Terminal(id))
	transcript := filepath.Join(s.Path("shop"), "cleared.jsonl")
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	s.StartCrashingAt(sessionmonitor.CrashSwitchPending)
	_ = s.Client().ObserveAgentConversation(terminal, "a-cleared-conversation", transcript)
	s.AwaitCrash()

	s.Start()
	app := s.App()
	successor, found := successorIn(app.Initial.Sessions, id)
	if !found {
		successor = awaitSuccessor(app, id)
	}
	if got := app.Terminal(string(successor.ID)); got != string(terminal) {
		t.Fatalf("after the restart the switch opened %s in terminal %q, want the terminal %s the harness reported from", successor.ID, got, terminal)
	}
}

func successorIn(sessions []protocol.Session, predecessor string) (protocol.Session, bool) {
	for _, s := range sessions {
		if string(protocol.Deref(s.Succeeds)) == predecessor {
			return s, true
		}
	}
	return protocol.Session{}, false
}

func restartedSession(app *testworld.Peer, id string) protocol.Session {
	app.T.Helper()
	for _, s := range app.Initial.Sessions {
		if string(s.ID) == id {
			return s
		}
	}
	app.T.Fatalf("after the restart the app lists no session %s", id)
	return protocol.Session{}
}

func TestASnoozeTheMonitorSavedIsStillSnoozedAfterARestart(t *testing.T) {
	t.Parallel()
	s, id := waitingAgentStack(t)
	until := time.Now().Add(time.Hour)
	snoozeUntil(s.App(), id, until)
	testworld.AwaitSession(s.App(), id, func(x protocol.Session) bool { return snoozedUntil(x).Equal(until) })
	s.Stop()

	s.Start()
	if got := snoozedUntil(restartedSession(s.App(), id)); !got.Equal(until) {
		t.Fatalf("after the restart the session is snoozed until %v, want %v, as its monitor saved", got, until)
	}
}
