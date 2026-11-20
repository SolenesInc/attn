package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func sigmaOutpostPair(t *testing.T) *testworld.OutpostPair {
	t.Helper()
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	requireStdout(t, home.Attn("settings", "set", "sigma_enabled", "true"), "true")
	awaitAgentAvailable(outpost.App(), fakeagent.Sigma)
	return pair
}

func requireNoHomeSigmaServer(t *testing.T, pair *testworld.OutpostPair) {
	t.Helper()
	if servers := pair.Home.AppServers(fakeagent.Sigma); servers != 0 {
		t.Fatalf("the home started %d Sigma servers, want none", servers)
	}
}

func outpostSigmaAtWork(t *testing.T, pair *testworld.OutpostPair) (string, *fakeagent.AppServer, string) {
	t.Helper()
	id := spawnOnOutpost(pair, fakeagent.Sigma)
	tui := pair.Outpost.LaunchedCarrying(pair.App.Terminal(id))
	server := pair.Outpost.AppServer(fakeagent.Sigma)
	pair.App.TypeLine(id, "work on the outpost")
	if got := server.Prompted(tui.ConversationID).Text; got != "work on the outpost" {
		t.Fatalf("the outpost's Sigma server took %q", got)
	}
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	return id, server, tui.ConversationID
}

func spawnOnOutpost(pair *testworld.OutpostPair, h fakeagent.Harness) string {
	return pair.Home.Spawn(pair.App, h, pair.Outpost.Path("agent"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
}

func TestAnOutpostSigmaSessionRunsOnTheOutpostsServerAndReportsToTheHome(t *testing.T) {
	pair := sigmaOutpostPair(t)
	id, server, conversation := outpostSigmaAtWork(t, pair)
	server.Reply(conversation, "Outpost ready. <!-- attn:state=waiting_input -->")
	session := testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
	})
	if protocol.Deref(session.Host) != pair.Host || session.Agent != "sigma" {
		t.Fatalf("session = host %q agent %q, want sigma on %q", protocol.Deref(session.Host), session.Agent, pair.Host)
	}
	requireNoHomeSigmaServer(t, pair)
	t.Logf("sigma terminal=%s host=%s conversation=%s state=%s; the outpost's server took the prompt, the home started none", pair.App.Terminal(id), pair.Host, conversation, session.State)
}

func TestAMessageForAnOfflineOutpostSigmaSessionArrivesAfterItReconnects(t *testing.T) {
	pair := sigmaOutpostPair(t)
	app := pair.App
	id, server, conversation := outpostSigmaAtWork(t, pair)
	server.Reply(conversation, "Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	sender := pair.Home.Spawn(app, fakeagent.Harness("shell"), pair.Home.Path("sender"))
	mark := app.EventCount()
	requireStdout(t, pair.Home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachUnreachable)
	message, err := pair.Home.Client().AgentMsg(id, protocol.SessionID(sender), "continue when connected")
	if err != nil || message == nil || !strings.Contains(message.Detail, "outpost, which is unreachable") {
		t.Fatalf("offline message = %+v, %v", message, err)
	}
	mark = app.EventCount()
	requireStdout(t, pair.Home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	outpostReachSince(app, mark, "outpost", protocol.OutpostReachReachable)
	if prompt := server.Prompted(conversation).Text; !strings.Contains(prompt, "attn") {
		t.Fatalf("queued inbox ring = %q", prompt)
	}
	testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Connected. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	requireNoHomeSigmaServer(t, pair)
	t.Logf("offline message: %s; the ring reached the outpost's Sigma server after it reconnected", message.Detail)
}

func TestAHomeRestartWhileAnOutpostSigmaSessionWorksStartsNoHomeSigmaServer(t *testing.T) {
	pair := sigmaOutpostPair(t)
	id, server, conversation := outpostSigmaAtWork(t, pair)
	pair.Home.Stop()
	pair.Home.Start()
	app := pair.Home.App()
	outpostStateOnConnect(app, "outpost", protocol.OutpostStateConnected)
	testworld.AwaitSessionFromConnect(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	mark := app.EventCount()
	server.Reply(conversation, "Done while the home restarted. <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, id, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
	})
	app.TypeLine(id, "after the home restart")
	if got := server.Prompted(conversation).Text; got != "after the home restart" {
		t.Fatalf("the outpost's Sigma server took %q after the home restart", got)
	}
	requireNoHomeSigmaServer(t, pair)
	t.Log("the home came back, the outpost's server kept the run and took the next prompt, and the home started no Sigma server")
}

func TestClosingAnOutpostSigmaSessionInterruptsItsRunOnTheOutpostsServer(t *testing.T) {
	pair := sigmaOutpostPair(t)
	id, server, conversation := outpostSigmaAtWork(t, pair)
	if err := pair.Home.Client().Unregister(protocol.SessionID(id)); err != nil {
		t.Fatal(err)
	}
	testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
	})
	server.AwaitInterrupted(conversation)
	requireNoHomeSigmaServer(t, pair)
	t.Logf("closing %s at the home interrupted conversation %s on the outpost's Sigma server", id, conversation)
}

func TestAnOutpostSigmaSessionThatClosesCompactingCompactsOnTheOutpostsServerAndThenCloses(t *testing.T) {
	pair := sigmaOutpostPair(t)
	id, server, conversation := outpostSigmaAtWork(t, pair)
	server.AnswerNextCompaction(conversation, fakeagent.ServerCompaction{Outcome: "compacted"})
	asked := pair.Outpost.Run(testworld.Invocation{Args: []string{"agent", "close", "--self", "--compact", "-m", compactOutpostReason}, Terminal: pair.App.Terminal(id)})
	if asked.Code != 0 {
		t.Fatalf("agent close --self --compact on the outpost exited %d: %s%s", asked.Code, asked.Stdout, asked.Stderr)
	}
	server.Reply(conversation, "Fixed and closing. <!-- attn:state=idle -->")
	closed := testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && string(e.SessionLedgerEntry.ID) == id
	})
	if got := protocol.Deref(closed.SessionLedgerEntry.CloseReason); got != compactOutpostReason {
		t.Fatalf("the session closed for %q, want %q", got, compactOutpostReason)
	}
	if compacted, duringARun := server.Compactions(conversation); compacted != 1 || duringARun != 0 {
		t.Fatalf("the outpost's Sigma server compacted %d times, %d during a run, want once after the run", compacted, duringARun)
	}
	requireNoHomeSigmaServer(t, pair)
	t.Logf("%s compacted once on the outpost's Sigma server after its run, then closed for %q", id, compactOutpostReason)
}

func TestTheSigmaModelListForAnOutpostComesFromTheOutpostsServer(t *testing.T) {
	pair := sigmaOutpostPair(t)
	sigma := "sigma"
	r := testworld.Request(pair.App, protocol.AgentCatalogMessage{Cmd: protocol.CmdAgentCatalog, Host: pair.Host, ModelsFor: &sigma, RequestID: protocol.Ptr("models")},
		protocol.EventAgentCatalogResult, func(r protocol.AgentCatalogResultMessage) bool { return protocol.Deref(r.RequestID) == "models" })
	if !r.Success || r.Catalog == nil || len(r.Catalog.Models) == 0 {
		t.Fatalf("agent_catalog for sigma on the outpost = %+v", r)
	}
	if servers := pair.Outpost.AppServers(fakeagent.Sigma); servers != 1 {
		t.Fatalf("the outpost started %d Sigma servers to list models, want one", servers)
	}
	requireNoHomeSigmaServer(t, pair)
	t.Logf("%d Sigma models from the outpost's server (%s/%s first); the home started none", len(r.Catalog.Models), r.Catalog.Models[0].Provider, r.Catalog.Models[0].ID)
}

func TestAHarnessIsRefusedOnAnOutpostOnlyWhenTheOutpostsCatalogLacksIt(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost.Vars = append(outpost.Vars, "ATTN_SIGMA_EXECUTABLE="+outpost.Path("no-sigma-here"))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	requireStdout(t, home.Attn("settings", "set", "sigma_enabled", "true"), "true")
	if outpostApp := outpost.App(); outpostApp.Initial.Settings["sigma_available"] != "false" {
		testworld.Await(outpostApp, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["sigma_available"] == "false" })
	}
	withHost := func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) }
	denied, _, _ := home.RequestSpawn(pair.App, fakeagent.Sigma, outpost.Path("agent"), withHost)
	if want := `agent "sigma" is not available on`; denied.Success || !strings.Contains(protocol.Deref(denied.Error), want) {
		t.Fatalf("Sigma on an outpost without it = %v %q, want %q", denied.Success, protocol.Deref(denied.Error), want)
	}
	if servers := home.AppServers(fakeagent.Sigma); servers != 0 {
		t.Fatalf("the refused launch started %d Sigma servers at the home", servers)
	}
	id := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("agent"), withHost)
	outpost.LaunchedCarrying(pair.App.Terminal(id))
	t.Logf("refused by the outpost's catalog: %s; Claude, which the outpost offers, launches there", protocol.Deref(denied.Error))
}
