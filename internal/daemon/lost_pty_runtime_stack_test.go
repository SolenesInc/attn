package daemon_test

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryTerminalComesBackWhenItsSharedHostDies(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex), testworld.OnPTYBackend(testworld.SharedHostPTY))
	s.Start()
	app := s.App()
	setSetting(t, app, "pty_shared_host_enabled", "false")
	onWorker := s.Spawn(app, fakeagent.Claude, s.Path("worker"))
	workerRun := lostRuntimeAgentAtRest(s, app, onWorker)
	s.EnableSharedHost(app)
	shop := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	api := s.Spawn(app, fakeagent.Codex, s.Path("api"))
	agents := map[string]*fakeagent.Run{shop: lostRuntimeAgentAtRest(s, app, shop), api: lostRuntimeAgentAtRest(s, app, api)}
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shell"))
	shopTerminal := protocol.TerminalID(app.Terminal(shop))
	shellTerminal := protocol.TerminalID(app.Terminal(shell))
	app.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: shopTerminal, Cols: 101, Rows: 37})
	testworld.Await(app, protocol.EventPtyResized, func(m protocol.PtyResizedMessage) bool { return m.ID == shopTerminal && m.Cols == 101 })
	mark := len(app.Log())

	s.KillRuntimeOf(string(shopTerminal))

	for session, first := range agents {
		revived := s.Launched(session)
		if !revived.Resumed || revived.ConversationID != first.ConversationID {
			t.Errorf("session %s came back as %q, want it resuming conversation %s", session, revived.Argv, first.ConversationID)
		}
		app.TypeLine(session, "still there?")
		if prompt := revived.Prompted(); prompt != "still there?" {
			t.Errorf("session %s was prompted %q, want what was typed", session, prompt)
		}
		revived.Reply("Here. <!-- attn:state=idle -->")
		testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	}
	testworld.AwaitSince(app, mark, protocol.EventRuntimeRespawned, func(m protocol.RuntimeRespawnedMessage) bool { return m.ID == shellTerminal })
	attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: shopTerminal},
		protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return r.ID == shopTerminal })
	if protocol.Deref(attached.Cols) != 101 || protocol.Deref(attached.Rows) != 37 {
		t.Errorf("session %s came back at %dx%d, want the 101x37 its pane asked for", shop, protocol.Deref(attached.Cols), protocol.Deref(attached.Rows))
	}
	app.TypeLine(onWorker, "worker still there?")
	if prompt := workerRun.Prompted(); prompt != "worker still there?" {
		t.Errorf("the agent on a worker was prompted %q, want it untouched by the host's death", prompt)
	}
	for _, event := range app.Received() {
		if event.Event == protocol.EventSessionExited {
			t.Errorf("the app was told terminal %s exited, want it brought back", protocol.Deref(event.ID))
		}
	}
}

func lostRuntimeAgentAtRest(s *testworld.Stack, app *testworld.Peer, session string) *fakeagent.Run {
	s.T.Helper()
	run := s.Launched(session)
	app.Type(session, "add a discount field\r", userKeystroke)
	run.Prompted()
	run.Reply("Done. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	return run
}

func TestAnAppServerSessionStillShowsItsServersStatusAfterItsTUIRuntimeDies(t *testing.T) {
	t.Parallel()
	s, app, session, server, conversation := appServerAtWorkApartFromItsServer(t)
	server.AskApproval(conversation)
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStatePendingApproval })

	mark := len(app.Log())
	s.KillRuntimeOf(app.Terminal(session))

	if revived := s.Launched(session); revived.ConversationID != conversation {
		t.Fatalf("the TUI came back on conversation %s, want %s", revived.ConversationID, conversation)
	}
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateRecoverable })
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStatePendingApproval })
	if servers := s.AppServers(fakeagent.Nisse); servers != 1 {
		t.Errorf("AppServer servers launched = %d, want the one still waiting on the approval", servers)
	}

	mark = len(app.Log())
	server.Work(conversation)
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Renamed. Keep the old view? <!-- attn:state=waiting_input -->")
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
}

func TestAnAppServerSessionAtRestComesBackAtRestAfterItsTUIRuntimeDies(t *testing.T) {
	t.Parallel()
	s, app, session, server, conversation := appServerAtWorkApartFromItsServer(t)
	server.Reply(conversation, "Renamed. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })

	mark := len(app.Log())
	s.KillRuntimeOf(app.Terminal(session))

	revived := s.Launched(session)
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateRecoverable })
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })

	mark = len(app.Log())
	app.TypeLine(session, "and the view?")
	revived.Prompted()
	server.Prompted(conversation)
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Gone too. <!-- attn:state=idle -->")
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
}

func TestAnAppServerSessionStillWaitsOnItsQuestionAfterItsTUIRuntimeDies(t *testing.T) {
	t.Parallel()
	s, app, session, server, conversation := appServerAtWorkApartFromItsServer(t)
	server.Reply(conversation, "By date or by customer? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })

	mark := len(app.Log())
	s.KillRuntimeOf(app.Terminal(session))

	s.Launched(session)
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateRecoverable })
	settled := testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool {
		return x.State == protocol.SessionStateWaitingInput || x.State == protocol.SessionStateIdle
	})
	if settled.State != protocol.SessionStateWaitingInput {
		t.Fatalf("the revived session settled %s, want it still waiting on its question", settled.State)
	}
}

func appServerAtWorkApartFromItsServer(t *testing.T) (*testworld.Stack, *testworld.Peer, string, *fakeagent.AppServer, string) {
	t.Helper()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Nisse), testworld.OnPTYBackend(testworld.SharedHostPTY))
	s.Start()
	app := s.App()
	startLastRunTestDriver(t, s, app)
	setSetting(t, app, "pty_shared_host_enabled", "false")
	s.Spawn(app, fakeagent.Harness(serverPluginAgent), s.Path("server-on-a-worker"))
	s.EnableSharedHost(app)
	session := s.Spawn(app, fakeagent.Harness(serverPluginAgent), s.Path("shop"))
	tui := s.Launched(session)
	server := s.AppServer(fakeagent.Nisse)
	conversation := tui.ConversationID
	app.Type(session, "rename the orders table\r", userKeystroke)
	tui.Prompted()
	server.Prompted(conversation)
	return s, app, session, server, conversation
}

func startLastRunTestDriver(t *testing.T, s *testworld.Stack, app *testworld.Peer) {
	t.Helper()
	w := &world{World: s.World}
	declaration := map[string]any{
		"protocol": "attn-app-server-v1", "executable": filepath.Join(s.Dir, "bin", "nisse"),
		"serve_args": []string{"serve"}, "home_env": "NISSE_HOME", "default_home": ".nisse", "socket_name": "nisse.sock",
		"initialize": map[string]any{"protocol": map[string]int{"min": 7}}, "caller_conversation_env": "NISSE_ROOT_CONVERSATION",
	}
	declaration["recovery"] = "last-run"
	declaration["on_close"] = "interrupt"
	driver := registerServerDriver(t, w, "last-run-server", declaration)
	driver.mu.Lock()
	driver.holdLaunch = false
	driver.serverExecutable = filepath.Join(s.Dir, "bin", "nisse")
	driver.mu.Unlock()
	awaitDriverAvailable(app, serverPluginAgent)
}

func TestAnAppServerSessionSurvivesAStartupWithoutItsPluginOrViewRuntime(t *testing.T) {
	s, app, session, server, conversation := appServerAtWorkApartFromItsServer(t)
	before := testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	terminal := app.Terminal(session)
	s.Stop()
	s.KillRuntimeOf(terminal)
	server.AskApproval(conversation)
	s.Start()
	app = s.App()
	kept := testworld.AwaitSessionFromConnect(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateRecoverable })
	if kept.ProfileID != before.ProfileID {
		t.Fatal("startup moved the session to another profile")
	}
	startLastRunTestDriver(t, s, app)
	if view := s.Launched(session); view.ConversationID != conversation {
		t.Fatalf("registration opened conversation %s, want %s", view.ConversationID, conversation)
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStatePendingApproval })
	if servers := s.AppServers(fakeagent.Nisse); servers != 1 {
		t.Fatalf("registration started %d servers, want the existing one", servers)
	}
}
