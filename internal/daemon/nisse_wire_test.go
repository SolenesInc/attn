package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func nisseSession(t *testing.T, w *world, app *testworld.Peer, opts ...func(*protocol.SpawnSessionMessage)) (string, *fakeagent.Run, *fakeagent.NisseServer) {
	t.Helper()
	setSetting(t, app, "nisse_enabled", "true")
	session := w.Spawn(app, fakeagent.Nisse, w.Path("shop"), opts...)
	return session, w.Launched(session), w.NisseServer()
}

func TestNisseIsOfferedOnlyWhileItsExperimentalSettingIsOn(t *testing.T) {
	w := newWorld(t, fakeagent.Nisse)
	app := w.App()
	if got, listed := app.Initial.Settings["nisse_available"]; listed {
		t.Fatalf("with Nisse off the app sees nisse_available = %q, want Nisse not offered", got)
	}
	refused, _, _ := w.RequestSpawn(app, fakeagent.Nisse, w.Path("shop"))
	if refused.Success {
		t.Fatalf("a Nisse launch with Nisse off = %+v, want it refused", refused)
	}

	setSetting(t, app, "nisse_enabled", "true")
	testworld.Await(app, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["nisse_available"] == "true"
	})
}

func TestANisseSessionRunsItsTUIOnTheConversationAttnNamedAndFollowsTheServersStatus(t *testing.T) {
	w := newWorld(t, fakeagent.Nisse)
	app := w.App()
	session, nisse, server := nisseSession(t, w, app, func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("find the flaky checkout test")
	})
	conversation := nisse.ConversationID
	if at := slices.Index(nisse.Argv, "--conversation"); at < 0 || nisse.Argv[at+1] != conversation || server.Started(conversation).Cwd != w.Path("shop") {
		t.Fatalf("the Nisse TUI ran %q on conversation %s, want it to start the conversation attn named in the session's folder", nisse.Argv, conversation)
	}
	if got := nisse.Prompted(); got != "find the flaky checkout test" {
		t.Fatalf("the TUI sent %q, want the initial prompt", got)
	}
	if got := server.Prompted(conversation).Text; got != "find the flaky checkout test" {
		t.Fatalf("the conversation took %q, want the initial prompt", got)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	server.AskApproval(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	server.Work(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "It races the tax lookup. Add a lock? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	app.TypeLine(session, "yes, add it")
	if got := nisse.Prompted(); got != "yes, add it" {
		t.Fatalf("the TUI sent %q", got)
	}
	server.Prompted(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Locked it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if servers := w.NisseServers(); servers != 1 {
		t.Errorf("Nisse servers launched = %d, want one", servers)
	}
}

func TestANisseRunThatFailsWaitsForTheUser(t *testing.T) {
	w := newWorld(t, fakeagent.Nisse)
	app := w.App()
	session, nisse, server := nisseSession(t, w, app)
	conversation := nisse.ConversationID
	app.TypeLine(session, "deploy it")
	nisse.Prompted()
	server.Prompted(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Fail(conversation, "the provider refused the request")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestFeedbackReachesANisseSessionAsAFollowUpFromAttn(t *testing.T) {
	w := newWorld(t, fakeagent.Nisse)
	app := w.App()
	session, nisse, server := nisseSession(t, w, app)
	conversation := nisse.ConversationID
	if got := submitSessionAnnotationFeedback(app, session, sessionAnnotationFeedback); !got.success || got.status != "delivered" {
		t.Fatalf("feedback to the Nisse session = %+v, want it delivered", got)
	}
	submitted := server.Prompted(conversation)
	if submitted.Text != sessionAnnotationFeedback || submitted.RequestID == "" {
		t.Fatalf("the conversation took %+v, want the feedback under the delivery's id", submitted)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Addressed. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestClosingANisseSessionInterruptsItsRunAndLetsTheServerGo(t *testing.T) {
	w := newWorld(t, fakeagent.Nisse)
	app := w.App()
	session, nisse, server := nisseSession(t, w, app)
	conversation := nisse.ConversationID
	app.TypeLine(session, "rewrite the billing module")
	nisse.Prompted()
	server.Prompted(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatalf("close the Nisse session: %s", protocol.Deref(closed.Error))
	}
	server.AwaitInterrupted(conversation)
	server.AwaitStopped()
}

func TestAResumedNisseSessionOpensTheConversationItLaunchedWith(t *testing.T) {
	w := newWorld(t, fakeagent.Nisse)
	app := w.App()
	session, nisse, server := nisseSession(t, w, app, func(m *protocol.SpawnSessionMessage) {
		m.Model = protocol.Ptr("openai/gpt-5.5")
		m.YoloMode = protocol.Ptr(true)
	})
	conversation := nisse.ConversationID
	started := server.Started(conversation)
	if started.Provider != "openai" || started.Model != "gpt-5.5" || started.Approvals != "auto" {
		t.Fatalf("the conversation started as %+v, want openai's gpt-5.5 running without asking", started)
	}
	if !nisse.Yolo {
		t.Errorf("the TUI ran %q, want --auto", nisse.Argv)
	}
	if closed := closeFromApp(app, session); !closed.Accepted {
		t.Fatalf("close: %s", protocol.Deref(closed.Error))
	}
	nisse.AwaitGone()

	resumed, _, _ := w.RequestSpawn(app, fakeagent.Nisse, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(conversation)
	})
	if !resumed.Success {
		t.Fatalf("resume the Nisse session: %s", protocol.Deref(resumed.Error))
	}
	again := w.Launched(string(resumed.ID))
	if again.ConversationID != conversation {
		t.Fatalf("the resumed TUI opened %s, want conversation %s", again.ConversationID, conversation)
	}
	app.TypeLine(string(resumed.ID), "carry on")
	again.Prompted()
	if got := w.NisseServer().Prompted(conversation).Text; got != "carry on" {
		t.Fatalf("the conversation took %q", got)
	}
}

func TestANisseSessionWorkingThroughADaemonRestartSettlesFromTheServerAfterIt(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
	w := newWorld(t, fakeagent.Nisse)
	t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
	app := w.App()
	session, nisse, server := nisseSession(t, w, app)
	conversation := nisse.ConversationID
	app.TypeLine(session, "migrate the orders table")
	nisse.Prompted()
	server.Prompted(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	w.restart()
	app = w.App()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Migrated. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if servers := w.NisseServers(); servers != 1 {
		t.Errorf("Nisse servers launched = %d, want the one that ran through the restart", servers)
	}
}

func TestQuittingTheNisseTUIClosesTheSessionAndInterruptsItsRun(t *testing.T) {
	w := newWorld(t, fakeagent.Nisse)
	app := w.App()
	session, nisse, server := nisseSession(t, w, app)
	conversation := nisse.ConversationID
	app.TypeLine(session, "rename the orders table")
	nisse.Prompted()
	server.Prompted(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	nisse.Exit(0)
	server.AwaitInterrupted(conversation)
	if entry := ledgerShowOverTheWebSocket(app, session).Entry; entry == nil || protocol.Deref(entry.ClosedAt) == "" {
		t.Errorf("the ledger shows %+v, want %s closed", entry, session)
	}
}

func TestANisseConversationOpenInAnotherProfileIsRefusedThere(t *testing.T) {
	w := newWorld(t, fakeagent.Nisse)
	app := w.App()
	setSetting(t, app, "nisse_enabled", "true")
	side := createProfile(app, "Side")
	sideApp := w.AppOn(side.ID)
	sideNisse := w.Launched(w.Spawn(sideApp, fakeagent.Nisse, w.Path("shop")))

	refused, _, _ := w.RequestSpawn(app, fakeagent.Nisse, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(sideNisse.ConversationID)
	})
	if refused.Success || !strings.Contains(protocol.Deref(refused.Error), side.ID) {
		t.Errorf("resuming %s here while profile %s holds it = %+v, want it refused naming that profile", sideNisse.ConversationID, side.ID, refused)
	}
}
