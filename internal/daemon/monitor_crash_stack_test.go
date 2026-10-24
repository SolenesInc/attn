package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

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
