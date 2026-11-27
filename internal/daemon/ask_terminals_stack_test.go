package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func askHolds(bindings []protocol.TerminalBinding, askID string, session protocol.SessionID) bool {
	return slices.ContainsFunc(bindings, func(b protocol.TerminalBinding) bool {
		return b.SessionID == session && protocol.Deref(b.AskID) == askID
	})
}

func awaitAskHolds(app *testworld.Peer, askID string, session protocol.SessionID) {
	app.T.Helper()
	testworld.Await(app, protocol.EventTerminalBindingsUpdated, func(m protocol.TerminalBindingsUpdatedMessage) bool {
		return askHolds(m.TerminalBindings, askID, session)
	})
}

func sessionListed(t *testing.T, cli *client.Client, id protocol.SessionID) bool {
	t.Helper()
	sessions, err := cli.Query("")
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	return slices.ContainsFunc(sessions, func(s protocol.Session) bool { return s.ID == id })
}

func spawnInAsk(s *testworld.Stack, app *testworld.Peer, cwd, askID string, opts ...func(*protocol.SpawnSessionMessage)) protocol.SessionID {
	s.T.Helper()
	return protocol.SessionID(s.Spawn(app, shellHarness, cwd, append(opts, func(m *protocol.SpawnSessionMessage) {
		m.Placement = &protocol.SessionPlacement{AskID: protocol.Ptr(askID)}
	})...))
}

func TestAnAskHeldShellOutlivesARestartAndEndsWhenItsAskClosedWhileTheDaemonWasDown(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	settled := s.PassesOf(pausepoint.AskTerminalsSettled)
	s.Start()
	<-settled
	app := s.App()
	setSetting(t, app, "asks_enabled", "true")
	asker := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	asked, err := s.Client().AskOpen(protocol.SessionID(asker), "", cacheAsk, "", s.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	shell := spawnInAsk(s, app, s.Path("shell"), asked.ID)
	awaitAskHolds(app, asked.ID, shell)

	s.Stop()
	s.Start()
	<-settled
	if !askHolds(s.App().Initial.TerminalBindings, asked.ID, shell) || !sessionListed(t, s.Client(), shell) {
		t.Fatalf("after a restart ask %s no longer holds shell %s", asked.ID, shell)
	}

	s.Stop()
	s.StartCrashingAt(pausepoint.AskTerminalsSettling)
	<-settled
	s.App().Send(protocol.AskAnswerMessage{Cmd: protocol.CmdAskAnswer, RequestID: "answer", AskID: asked.ID, Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Postgres"}}}})
	s.AwaitCrash()
	s.Start()
	<-settled
	if sessionListed(t, s.Client(), shell) {
		t.Errorf("ask %s was answered just before the daemon went down, and the next daemon kept its shell %s", asked.ID, shell)
	}
}

func TestAnOutpostKeepsItsAskHeldShellThroughItsRestartAndTheHomeEndsIt(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t)
	recovered := home.PauseEvery(pausepoint.OutpostRecoveryApplied)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	recovered.Await()
	recovered.Release()
	app, cli := pair.App, home.Client()
	setSetting(t, app, "asks_enabled", "true")
	asker := home.Spawn(app, fakeagent.Claude, home.Path("shop"))
	asked, err := cli.AskOpen(protocol.SessionID(asker), "", cacheAsk, "", home.Path("shop"))
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	shell := spawnInAsk(home, app, outpost.Path("far"), asked.ID, func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	awaitAskHolds(app, asked.ID, shell)

	mark := len(app.Log())
	pair.Disconnect()
	awaitOutpostState(app, mark, pair.Host, protocol.OutpostStateLost)
	outpost.Stop()
	settled := outpost.PauseAt(pausepoint.AskTerminalsSettled)
	outpost.StartHeldAt(settled)
	settled.Release()
	pair.Reconnect()
	stopRetries := home.FireTimers(bridge.RetryTimer)
	recovered.Await()
	recovered.Release()
	stopRetries()
	if !sessionListed(t, cli, shell) {
		t.Fatalf("the outpost's restart ended shell %s, which ask %s still holds", shell, asked.ID)
	}

	answerCacheAsk(t, app, asked.ID)
	awaitClosed(app, string(shell))
}
