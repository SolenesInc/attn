package main_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAgentsThatDieAfterADaemonRestartBreakThroughTheirSnooze(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	agents := map[string]*fakeagent.Run{}
	for _, repo := range []string{"cart", "checkout", "invoices"} {
		id := s.Spawn(app, fakeagent.Claude, s.Path(repo))
		agent := s.Launched(id)
		agents[id] = agent
		app.TypeLine(id, "add a discount field")
		agent.Prompted()
		agent.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
		testworld.AwaitSession(app, id, func(x protocol.Session) bool {
			return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
		})
		until := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
		app.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: id, Until: until})
		testworld.AwaitSession(app, id, func(x protocol.Session) bool { return protocol.Deref(x.TurnSnoozedUntil) != "" })
	}

	s.Stop()
	s.Start()
	app = s.App()
	for id, agent := range agents {
		agent.Exit(1)
		testworld.AwaitSession(app, id, func(x protocol.Session) bool {
			return protocol.Deref(x.TurnOwed) && protocol.Deref(x.TurnSnoozedUntil) == ""
		})
	}
}
