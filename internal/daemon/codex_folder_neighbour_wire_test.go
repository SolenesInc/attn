package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACodexSessionThatExitsWithoutAConversationIsNotJudgedFromItsNeighbours(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := w.Path("shop")
	earlier := w.Spawn(app, fakeagent.Codex, cwd)
	codex := w.Launched(earlier)
	app.TypeLine(earlier, "find the flaky test")
	codex.Prompted()
	codex.Reply("Which test runner do you use? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, earlier, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	unused := w.Spawn(app, fakeagent.Codex, cwd)
	w.Launched(unused).Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == unused })

	for _, o := range stateExplainOf(t, cli, unused).Observations {
		if o.Source == "classifier" && o.Claim == string(protocol.SessionStateWaitingInput) {
			t.Fatalf("%s, which never had a conversation, was judged %s from %s's conversation", unused, o.Claim, earlier)
		}
	}
}
