package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func sigmaOutpostPair(t *testing.T, beforeStart ...func(*testworld.OutpostPair)) *testworld.OutpostPair {
	t.Helper()
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	for _, fn := range beforeStart {
		fn(pair)
	}
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
	t.Logf("%d Sigma models from the outpost's server (%s first); the home started none", len(r.Catalog.Models), r.Catalog.Models[0].ID)
}

func TestAHarnessIsRefusedOnAnOutpostOnlyWhenTheOutpostsCatalogLacksIt(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost.Vars = append(outpost.Vars, "ATTN_SIGMA_EXECUTABLE="+outpost.Path("no-sigma-here"))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	requireStdout(t, home.Attn("settings", "set", "sigma_enabled", "true"), "true")
	testworld.Await(outpost.App(), protocol.EventHarnessCatalog, func(m protocol.HarnessCatalogMessage) bool {
		h, ok := testworld.FindHarness(m.Harnesses, "sigma")
		return ok && h.Offered && h.Installation == protocol.HarnessInstallationMissing
	})
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

func TestAnOpenOutpostSigmaSessionRevivesAfterSigmaIsTurnedOffWhileANewOneIsRefused(t *testing.T) {
	pair := sigmaOutpostPair(t)
	app := pair.App
	id := spawnOnOutpost(pair, fakeagent.Sigma)
	terminal := app.Terminal(id)
	tui := pair.Outpost.LaunchedCarrying(terminal)
	outpostApp := pair.Outpost.App()
	mark := outpostApp.EventCount()
	requireStdout(t, pair.Home.Attn("settings", "set", "sigma_enabled", "false"), "false")
	testworld.AwaitSince(outpostApp, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["sigma_enabled"] == "false" })
	tui.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.WebSocketEvent) bool {
		return protocol.Deref(e.SessionID) == protocol.SessionID(id) && protocol.Deref(e.ExitCode) == 1
	})
	mark = app.EventCount()
	app.Send(protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal), AttachPolicy: protocol.Ptr(protocol.AttachPolicyRevive), Cols: protocol.Ptr(100), Rows: protocol.Ptr(30)})
	revived := testworld.AwaitSince(app, mark, protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal })
	if !revived.Success || !protocol.Deref(revived.Revived) {
		t.Fatalf("reviving the open Sigma session with sigma_enabled off failed: %s", protocol.Deref(revived.Error))
	}
	if again := pair.Outpost.LaunchedCarrying(terminal); again.ConversationID != tui.ConversationID {
		t.Fatalf("the revival resumed conversation %s, want %s", again.ConversationID, tui.ConversationID)
	}
	denied, _, _ := pair.Home.RequestSpawn(app, fakeagent.Sigma, pair.Outpost.Path("new"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	if want := `agent "sigma" is not available`; denied.Success || !strings.Contains(protocol.Deref(denied.Error), want) {
		t.Fatalf("a new Sigma launch on the outpost with sigma_enabled off = %v %q, want %q", denied.Success, protocol.Deref(denied.Error), want)
	}
	requireNoHomeSigmaServer(t, pair)
	t.Logf("with sigma_enabled off, %s revived on the outpost into conversation %s; a new launch was refused: %s", id, tui.ConversationID, protocol.Deref(denied.Error))
}

func awaitHomeAdoptedSession(app *testworld.Peer, label string) protocol.Session {
	app.T.Helper()
	for _, s := range app.Initial.Sessions {
		if s.Label == label {
			return s
		}
	}
	return *testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.Label == label
	}).Session
}

func homeSessionsWithLabel(t *testing.T, pair *testworld.OutpostPair, label string) []protocol.Session {
	t.Helper()
	list, err := pair.Home.Client().List("")
	if err != nil {
		t.Fatalf("list home sessions: %v", err)
	}
	var matches []protocol.Session
	for _, s := range list.Sessions {
		if s.Label == label {
			matches = append(matches, s)
		}
	}
	return matches
}

func TestAnOutpostSigmaWorkerAdoptsAtHomeAndActsAsItsOwnSessionOnTheOutpost(t *testing.T) {
	pair := sigmaOutpostPair(t)
	parent, server, parentConv := outpostSigmaAtWork(t, pair)
	mark := pair.App.EventCount()
	server.Reply(parentConv, "Parent ready. <!-- attn:state=idle -->")
	testworld.AwaitSessionSince(pair.App, mark, parent, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	var planted struct {
		ID string `json:"id"`
	}
	pair.Home.Run(testworld.Invocation{Args: []string{"seed", "plant", "outpost worker target", "--json"}}).JSON(t, &planted)
	if planted.ID == "" {
		t.Fatal("seed plant returned empty id")
	}

	server.Worker(parentConv, "outpost-worker", "audit")
	presence := pair.Outpost.Run(testworld.Invocation{
		Args: []string{"presence", "--session-id"},
		Env:  []string{"SIGMA_ROOT_CONVERSATION=outpost-worker"},
	})
	if presence.Code != 0 {
		t.Fatalf("outpost worker presence failed (%d): %s%s", presence.Code, presence.Stdout, presence.Stderr)
	}
	workerID := strings.TrimSpace(presence.Stdout)
	if workerID == "" || workerID == parent {
		t.Fatalf("outpost worker presence = %q, want distinct adopted worker session (parent=%s)", workerID, parent)
	}

	adopted := awaitHomeAdoptedSession(pair.App, "audit")
	if string(adopted.ID) != workerID {
		t.Errorf("home adopted worker id = %s, want %s from outpost presence", adopted.ID, workerID)
	}
	if protocol.Deref(adopted.Host) != pair.Host || adopted.Agent != "sigma" {
		t.Errorf("adopted worker metadata = host %q agent %q, want sigma on %q", protocol.Deref(adopted.Host), adopted.Agent, pair.Host)
	}
	testworld.AwaitSessionFromConnect(pair.App, workerID, func(s protocol.Session) bool {
		return !protocol.Deref(s.Hidden)
	})
	workerTile := pair.Outpost.LaunchedCarrying(pair.App.Terminal(workerID))
	if workerTile.ConversationID != "outpost-worker" {
		t.Errorf("outpost worker tile opened %q, want outpost-worker", workerTile.ConversationID)
	}

	note := pair.Outpost.Run(testworld.Invocation{
		Args: []string{"seed", "note", planted.ID, "-m", "Note from outpost worker."},
		Env:  []string{"SIGMA_ROOT_CONVERSATION=outpost-worker"},
	})
	if note.Code != 0 {
		t.Fatalf("outpost worker seed note failed (%d): %s%s", note.Code, note.Stdout, note.Stderr)
	}
	var notes struct {
		Notes []protocol.SeedNote `json:"notes"`
	}
	pair.Home.Run(testworld.Invocation{Args: []string{"seed", "notes", planted.ID, "--json"}}).JSON(t, &notes)
	if len(notes.Notes) == 0 || string(notes.Notes[0].Author.Ref) != "session:"+workerID {
		t.Fatalf("seed notes = %+v, want newest author.ref session:%s", notes.Notes, workerID)
	}

	mark = pair.App.EventCount()
	server.Work("outpost-worker")
	testworld.AwaitSessionSince(pair.App, mark, workerID, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	mark = pair.App.EventCount()
	server.Reply("outpost-worker", "Audit finished. <!-- attn:state=idle -->")
	testworld.AwaitSessionSince(pair.App, mark, workerID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if got := homeSessionsWithLabel(t, pair, "audit"); len(got) != 1 {
		t.Fatalf("home sessions titled audit = %d, want 1", len(got))
	}
	requireNoHomeSigmaServer(t, pair)
}

func TestAnOutpostSigmaWorkerCreatedWhileOfflineAdoptsWhenTheBridgeReturnsAndSurvivesOutpostRestart(t *testing.T) {
	var deferred *testworld.Pause
	pair := sigmaOutpostPair(t, func(p *testworld.OutpostPair) {
		deferred = p.Outpost.PauseAt(pausepoint.OutpostAdoptionDeferred)
	})
	outpostApp := pair.Outpost.App()
	parent, server, parentConv := outpostSigmaAtWork(t, pair)
	mark := pair.App.EventCount()
	server.Reply(parentConv, "Parent idle. <!-- attn:state=idle -->")
	testworld.AwaitSessionSince(pair.App, mark, parent, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	outpostMark := outpostApp.EventCount()
	mark = pair.App.EventCount()
	requireStdout(t, pair.Home.Attn("settings", "set", "outposts_enabled", "false"), "false")
	outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachUnreachable)
	testworld.AwaitSince(outpostApp, outpostMark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["outpost_state"] == "off"
	})

	server.Worker(parentConv, "offline-worker", "offline audit")
	server.Work("offline-worker")
	deferred.Await()
	deferred.Release()

	mark = pair.App.EventCount()
	requireStdout(t, pair.Home.Attn("settings", "set", "outposts_enabled", "true"), "true")
	outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachReachable)

	adopted := awaitHomeAdoptedSession(pair.App, "offline audit")
	workerID := string(adopted.ID)
	if protocol.Deref(adopted.Host) != pair.Host || adopted.Agent != "sigma" {
		t.Fatalf("offline-adopted worker = host %q agent %q, want sigma on %q", protocol.Deref(adopted.Host), adopted.Agent, pair.Host)
	}
	testworld.AwaitSessionFromConnect(pair.App, workerID, func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && !protocol.Deref(s.Hidden)
	})
	workerTile := pair.Outpost.LaunchedCarrying(pair.App.Terminal(workerID))
	if workerTile.ConversationID != "offline-worker" {
		t.Errorf("offline worker tile opened %q, want offline-worker", workerTile.ConversationID)
	}
	requireNoHomeSigmaServer(t, pair)

	mark = pair.App.EventCount()
	pair.Disconnect()
	outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachUnreachable)
	pair.Outpost.Stop()
	pair.Outpost.Start()
	mark = pair.App.EventCount()
	pair.Reconnect()
	stopRetries := pair.Home.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	outpostReachSince(pair.App, mark, "outpost", protocol.OutpostReachReachable)

	replyMark := pair.App.EventCount()
	server.Reply("offline-worker", "Done after outpost restart. <!-- attn:state=idle -->")
	testworld.AwaitSessionSince(pair.App, replyMark, workerID, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if got := homeSessionsWithLabel(t, pair, "offline audit"); len(got) != 1 {
		t.Fatalf("home sessions titled offline audit after outpost restart = %d, want 1", len(got))
	}
	requireNoHomeSigmaServer(t, pair)
}

func TestHomeAndOutpostSigmaWorkersWithTheSameConversationIDStaySeparate(t *testing.T) {
	pair := sigmaOutpostPair(t)
	awaitAgentAvailable(pair.App, fakeagent.Sigma)

	homeParent := pair.Home.Spawn(pair.App, fakeagent.Sigma, pair.Home.Path("home-agent"))
	homeTUI := pair.Home.LaunchedCarrying(pair.App.Terminal(homeParent))
	homeServer := pair.Home.AppServer(fakeagent.Sigma)
	pair.App.TypeLine(homeParent, "work at home")
	homeServer.Prompted(homeTUI.ConversationID)
	testworld.AwaitSession(pair.App, homeParent, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	mark := pair.App.EventCount()
	homeServer.Reply(homeTUI.ConversationID, "Home ready. <!-- attn:state=idle -->")
	testworld.AwaitSessionSince(pair.App, mark, homeParent, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	outpostParent, outpostServer, outpostConv := outpostSigmaAtWork(t, pair)
	mark = pair.App.EventCount()
	outpostServer.Reply(outpostConv, "Outpost ready. <!-- attn:state=idle -->")
	testworld.AwaitSessionSince(pair.App, mark, outpostParent, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	homeServer.Worker(homeTUI.ConversationID, "2", "home worker")
	homeServer.Work("2")
	outpostServer.Worker(outpostConv, "2", "outpost worker")
	outpostServer.Work("2")

	homeWorker := awaitHomeAdoptedSession(pair.App, "home worker")
	outpostWorker := awaitHomeAdoptedSession(pair.App, "outpost worker")
	if homeWorker.ID == outpostWorker.ID {
		t.Fatalf("home and outpost workers with conversation id 2 collided into %s", homeWorker.ID)
	}
	if protocol.Deref(homeWorker.Host) != "" || protocol.Deref(outpostWorker.Host) != pair.Host {
		t.Fatalf("hosts = home:%q outpost:%q, want \"\" and %q", protocol.Deref(homeWorker.Host), protocol.Deref(outpostWorker.Host), pair.Host)
	}
	testworld.AwaitSessionFromConnect(pair.App, string(homeWorker.ID), func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && !protocol.Deref(s.Hidden)
	})
	testworld.AwaitSessionFromConnect(pair.App, string(outpostWorker.ID), func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && !protocol.Deref(s.Hidden)
	})
	if got := pair.Home.LaunchedCarrying(pair.App.Terminal(string(homeWorker.ID))).ConversationID; got != "2" {
		t.Errorf("home worker tile opened %q, want 2", got)
	}
	if got := pair.Outpost.LaunchedCarrying(pair.App.Terminal(string(outpostWorker.ID))).ConversationID; got != "2" {
		t.Errorf("outpost worker tile opened %q, want 2", got)
	}

	homePresence := pair.Home.Run(testworld.Invocation{
		Args: []string{"presence", "--session-id"},
		Env:  []string{"SIGMA_ROOT_CONVERSATION=2"},
	})
	if homePresence.Code != 0 || strings.TrimSpace(homePresence.Stdout) != string(homeWorker.ID) {
		t.Fatalf("home presence for SIGMA_ROOT_CONVERSATION=2 = (%d) %q %s, want %s", homePresence.Code, homePresence.Stdout, homePresence.Stderr, homeWorker.ID)
	}
	outpostPresence := pair.Outpost.Run(testworld.Invocation{
		Args: []string{"presence", "--session-id"},
		Env:  []string{"SIGMA_ROOT_CONVERSATION=2"},
	})
	if outpostPresence.Code != 0 || strings.TrimSpace(outpostPresence.Stdout) != string(outpostWorker.ID) {
		t.Fatalf("outpost presence for SIGMA_ROOT_CONVERSATION=2 = (%d) %q %s, want %s", outpostPresence.Code, outpostPresence.Stdout, outpostPresence.Stderr, outpostWorker.ID)
	}

	for _, label := range []string{"home worker", "outpost worker"} {
		matches := homeSessionsWithLabel(t, pair, label)
		if len(matches) != 1 || len(matches[0].ConversationSharedWith) != 0 {
			t.Fatalf("%s session list = %+v, want 1 session with empty conversation_shared_with", label, matches)
		}
	}

	if err := pair.Home.Client().Unregister(outpostWorker.ID); err != nil {
		t.Fatalf("close outpost worker: %v", err)
	}
	testworld.Await(pair.App, protocol.EventSessionClosed, func(e protocol.WebSocketEvent) bool {
		return e.SessionLedgerEntry != nil && e.SessionLedgerEntry.ID == outpostWorker.ID
	})
	outpostServer.AwaitInterrupted("2")
	if still := homeSessionsWithLabel(t, pair, "home worker"); len(still) != 1 || still[0].State != protocol.SessionStateWorking {
		t.Fatalf("home worker after closing outpost worker = %+v, want still open and working", still)
	}
}

func TestAnAnnotationAndAReloadOfAnOutpostSigmaSessionStartNoHomeSigmaServer(t *testing.T) {
	pair := sigmaOutpostPair(t)
	id, server, conversation := outpostSigmaAtWork(t, pair)
	server.Reply(conversation, "Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return protocol.Deref(s.TurnOwed) })
	annotated := testworld.Request(pair.App, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "outpost-annotation", SessionID: protocol.SessionID(id), Text: "check the totals"},
		protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool {
			return r.RequestID == "outpost-annotation"
		})
	if !annotated.Success {
		t.Fatalf("annotation: %+v", annotated)
	}
	if got := server.Prompted(conversation); got.Text != "check the totals" {
		t.Fatalf("the outpost's server took %+v, want the annotation", got)
	}
	reloaded := testworld.Request(pair.App, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(id), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == id })
	if !reloaded.Success {
		t.Fatalf("reload: %s", protocol.Deref(reloaded.Error))
	}
	requireNoHomeSigmaServer(t, pair)
}

func TestAnIdleForkOfAnOutpostSigmaSessionMovesTheHomeTileToTheFork(t *testing.T) {
	pair := sigmaOutpostPair(t)
	id := spawnOnOutpost(pair, fakeagent.Sigma)
	terminal := pair.App.Terminal(id)
	tui := pair.Outpost.LaunchedCarrying(terminal)
	server := pair.Outpost.AppServer(fakeagent.Sigma)
	original := tui.ConversationID
	pair.App.TypeLine(id, "work on the outpost")
	tui.Prompted()
	server.Prompted(original)
	server.Reply(original, "Ready. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	mark := pair.App.EventCount()
	pair.App.TypeLine(id, "/fork")
	tui.Prompted()
	forks := server.Forks(original)
	if len(forks) != 1 {
		t.Fatalf("the /fork made %v, want one fork", forks)
	}
	var shown string
	testworld.AwaitSince(pair.App, mark, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
		for _, desktop := range e.Desktops {
			for _, pane := range desktop.Panes {
				if string(pane.RuntimeID) == terminal && string(pane.SessionID) != id {
					shown = string(pane.SessionID)
					return true
				}
			}
		}
		return false
	})
	workMark := pair.App.EventCount()
	pair.App.TypeLine(shown, "work in the fork")
	if got := server.Prompted(forks[0]); got.Text != "work in the fork" {
		t.Fatalf("the fork %s took %+v, want the prompt typed in the tile", forks[0], got)
	}
	testworld.AwaitSessionSince(pair.App, workMark, shown, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	requireNoHomeSigmaServer(t, pair)
}
