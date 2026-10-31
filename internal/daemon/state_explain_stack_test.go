package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestStateExplainNamesTheCauseOfAStateTheDaemonIsStillReactingTo(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	reacting := s.PauseAt(pausepoint.SessionStateReacting)
	s.Start()
	app, cli := s.App(), s.Client()
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	reacting.Await()
	shown := testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })

	explained := stateExplainOf(t, cli, session)
	if explained.State != string(shown.State) || protocol.Deref(explained.StateSince) != shown.StateSince {
		t.Fatalf("explain shows %s since %s, want %s since %s", explained.State, protocol.Deref(explained.StateSince), shown.State, shown.StateSince)
	}
	applied := false
	for _, obs := range explained.Observations {
		applied = applied || obs.Claim == explained.State && obs.Outcome == "applied"
	}
	if !applied {
		t.Fatalf("explain shows the session %s without the claim that applied it:\n%s", explained.State, describeStateExplain(explained.Observations))
	}
	reacting.Release()
}
