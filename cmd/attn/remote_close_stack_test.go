package main_test

import (
	"fmt"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnExitedOutpostAgentClosesWithItsFinalUsage(t *testing.T) {
	for _, harness := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		for _, code := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/exit-%d", harness, code), func(t *testing.T) {
				home := testworld.NewStack(t)
				outpost := testworld.NewStack(t, testworld.WithAgents(harness))
				pair := testworld.PrepareOutpostPair(t, home, outpost)
				pair.Start()
				awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
				outpost.SetCodexFolderTrust(outpost.Path("agent"), "trusted")
				id := home.Spawn(pair.App, harness, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
				agent := outpost.LaunchedCarrying(pair.App.Terminal(id))
				pair.App.TypeLine(id, "final work")
				agent.Prompted()
				reply := "Final answer before exiting. <!-- attn:state=waiting_input -->"
				agent.ReplyUnheard(reply)
				agent.Exit(code)
				if code != 0 {
					testworld.Await(pair.App, protocol.EventSessionExited, func(e protocol.WebSocketEvent) bool {
						return protocol.Deref(e.SessionID) == protocol.SessionID(id)
					})
					if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
						t.Fatal(err)
					}
				}
				closed := testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
					return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
				}).SessionLedgerEntry
				if closed.Usage == nil || closed.Usage.TotalTokens != 2*len(reply) {
					t.Fatalf("%s exit %d final usage = %+v, want %d tokens", harness, code, closed.Usage, 2*len(reply))
				}
				t.Logf("%s exit %d: ledger keeps %d tokens", harness, code, closed.Usage.TotalTokens)
			})
		}
	}
}

func TestAnInterruptedOutpostCodexCloseReleasesItsServerAfterReconnect(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	drained := outpost.PauseAt(pausepoint.AgentCloseDrained)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	shareOutpostCodex(t, pair)
	id := home.Spawn(pair.App, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	agent := outpost.LaunchedCarrying(pair.App.Terminal(id))
	pair.App.TypeLine(id, "before closing")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	server := outpost.CodexServer()
	other := home.Spawn(pair.App, fakeagent.Codex, outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	keeper := outpost.LaunchedCarrying(pair.App.Terminal(other))
	pair.App.TypeLine(other, "keep another conversation open")
	keeper.Prompted()
	keeper.Reply("Ready too. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, other, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	closing := make(chan error, 1)
	go func() { closing <- home.Client().Unregister(protocol.SessionID(id)) }()
	drained.Await()
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	if err := <-closing; err == nil {
		t.Fatal("close succeeded after losing its drain reply")
	}
	drained.Release()
	mark := len(pair.App.Log())
	requireStdout(t, home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	outpostStateSince(pair.App, "outpost", protocol.OutpostStateConnected, mark)
	if err := home.Client().Unregister(protocol.SessionID(id)); err != nil {
		t.Fatal(err)
	}
	server.AwaitArchived(agent.ConversationID)
	if err := home.Client().Unregister(protocol.SessionID(other)); err != nil {
		t.Fatal(err)
	}
	server.AwaitStopped()
	t.Log("lost drain reply; retried close archives the conversation and releases its unused Codex app-server")
}
