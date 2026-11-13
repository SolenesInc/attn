package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

var appServerHarnesses = []fakeagent.Harness{fakeagent.Sigma}

func eachAppServer(t *testing.T, test func(t *testing.T, h fakeagent.Harness)) {
	for _, h := range appServerHarnesses {
		t.Run(string(h), func(t *testing.T) { test(t, h) })
	}
}

func appServerSession(t *testing.T, w *world, app *testworld.Peer, h fakeagent.Harness, opts ...func(*protocol.SpawnSessionMessage)) (string, *fakeagent.Run, *fakeagent.AppServer) {
	t.Helper()
	registerSigmaDriver(t, w, app)
	session := w.Spawn(app, h, w.Path("shop"), opts...)
	return session, w.Launched(session), w.AppServer(h)
}

func promptUntilWorking(app *testworld.Peer, session string, nisse *fakeagent.Run, server *fakeagent.AppServer, prompt string) {
	app.T.Helper()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	mark := len(app.Log())
	app.TypeLine(session, prompt)
	nisse.Prompted()
	server.Prompted(nisse.ConversationID)
	testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func TestAnAppServerSessionRunsItsTUIOnTheConversationAttnNamedAndFollowsTheServersStatus(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, nisse, server := appServerSession(t, w, app, h, func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("find the flaky checkout test")
		})
		conversation := nisse.ConversationID
		if at := slices.Index(nisse.Argv, "--conversation"); at < 0 || nisse.Argv[at+1] != conversation || server.Started(conversation).Cwd != w.Path("shop") {
			t.Fatalf("the app-server TUI ran %q on conversation %s, want it to start the conversation attn named in the session's folder", nisse.Argv, conversation)
		}
		if got := nisse.Prompted(); got != "find the flaky checkout test" {
			t.Fatalf("the TUI sent %q, want the initial prompt", got)
		}
		if got := server.Prompted(conversation).Text; got != "find the flaky checkout test" {
			t.Fatalf("the conversation took %q, want the initial prompt", got)
		}
		started := testworld.AwaitSessionSince(app, 0, session, func(s protocol.Session) bool { return s.State != protocol.SessionStateLaunching })
		if started.State != protocol.SessionStateWorking {
			t.Fatalf("a session launched with a prompt first left launching for %s, want working", started.State)
		}

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
		if servers := w.AppServers(h); servers != 1 {
			t.Errorf("app-server servers launched = %d, want one", servers)
		}
	})
}

func TestAnAppServerRunThatFailsWaitsForTheUser(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, nisse, server := appServerSession(t, w, app, h)
		conversation := nisse.ConversationID
		app.TypeLine(session, "deploy it")
		nisse.Prompted()
		server.Prompted(conversation)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.Fail(conversation, "the provider refused the request")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	})
}

func TestFeedbackReachesAnAppServerSessionAsAFollowUpFromAttn(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, nisse, server := appServerSession(t, w, app, h)
		conversation := nisse.ConversationID
		if got := submitSessionAnnotationFeedback(app, session, sessionAnnotationFeedback); !got.success || got.status != "delivered" {
			t.Fatalf("feedback to the app-server session = %+v, want it delivered", got)
		}
		submitted := server.Prompted(conversation)
		if submitted.Text != sessionAnnotationFeedback || submitted.RequestID == "" {
			t.Fatalf("the conversation took %+v, want the feedback under the delivery's id", submitted)
		}
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.Reply(conversation, "Addressed. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	})
}

func TestAResumedAppServerSessionOpensTheConversationItLaunchedWith(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, nisse, server := appServerSession(t, w, app, h, func(m *protocol.SpawnSessionMessage) {
			m.Model = protocol.Ptr("openai/gpt-5.5")
			m.Effort = protocol.Ptr("high")
			m.YoloMode = protocol.Ptr(true)
		})
		conversation := nisse.ConversationID
		started := server.Started(conversation)
		if started.Provider != "openai" || started.Model != "gpt-5.5" || started.Effort != "high" || started.Approvals != "auto" {
			t.Fatalf("the conversation started as %+v, want openai's gpt-5.5 at high effort running without asking", started)
		}
		if !nisse.Yolo {
			t.Errorf("the TUI ran %q, want --auto", nisse.Argv)
		}
		if closed := closeFromApp(app, session); !closed.Accepted {
			t.Fatalf("close: %s", protocol.Deref(closed.Error))
		}
		nisse.AwaitGone()

		resumed, _, _ := w.RequestSpawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
			m.ResumeSessionID = protocol.Ptr(conversation)
		})
		if !resumed.Success {
			t.Fatalf("resume the app-server session: %s", protocol.Deref(resumed.Error))
		}
		again := w.Launched(string(resumed.ID))
		if again.ConversationID != conversation {
			t.Fatalf("the resumed TUI opened %s, want conversation %s", again.ConversationID, conversation)
		}
		app.TypeLine(string(resumed.ID), "carry on")
		again.Prompted()
		if got := w.AppServer(h).Prompted(conversation).Text; got != "carry on" {
			t.Fatalf("the conversation took %q", got)
		}
	})
}

func TestAReopenedAppServerSessionStillWaitsOnTheQuestionItAsked(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, nisse, server := appServerSession(t, w, app, h)
		neighbour := w.Spawn(app, h, w.Path("neighbour"))
		neighbourTUI := w.Launched(neighbour)
		conversation := nisse.ConversationID
		app.TypeLine(session, "which index should the report use?")
		nisse.Prompted()
		server.Prompted(conversation)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.Reply(conversation, "By date or by customer? <!-- attn:state=waiting_input -->")
		asked := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
		})

		if closed := closeFromApp(app, session); !closed.Accepted {
			t.Fatalf("close: %s", protocol.Deref(closed.Error))
		}
		nisse.AwaitGone()
		if reopened := reopenOverTheWebSocket(app, session); !reopened.Success {
			t.Fatalf("reopen: %s", protocol.Deref(reopened.Error))
		}
		again := w.Launched(session)

		mark := len(app.Log())
		app.TypeLine(neighbour, "keep the server connected")
		neighbourTUI.Prompted()
		server.Prompted(neighbourTUI.ConversationID)
		testworld.AwaitSessionSince(app, mark, neighbour, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		reopened := queriedSession(t, w.Client(), session)
		if reopened.State != protocol.SessionStateWaitingInput || protocol.Deref(reopened.TurnOpenedAt) != protocol.Deref(asked.TurnOpenedAt) {
			t.Fatalf("after reopening the session is %s with its turn opened at %s, want it still waiting on the turn opened at %s", reopened.State, protocol.Deref(reopened.TurnOpenedAt), protocol.Deref(asked.TurnOpenedAt))
		}

		mark = len(app.Log())
		app.TypeLine(session, "by date")
		again.Prompted()
		server.Prompted(conversation)
		testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.Reply(conversation, "Indexed by date. <!-- attn:state=idle -->")
		testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	})
}

func TestAnAppServerSessionWorkingThroughADaemonRestartSettlesFromTheServerAfterIt(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		t.Setenv("ATTN_PTY_BACKEND", "worker")
		t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
		w := newWorld(t, h)
		t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
		app := w.App()
		session, nisse, server := appServerSession(t, w, app, h)
		conversation := nisse.ConversationID
		promptUntilWorking(app, session, nisse, server, "migrate the orders table")

		w.restart()
		registerSigmaDriver(t, w, w.App())
		app = w.App()
		testworld.AwaitSessionFromConnect(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.Reply(conversation, "Migrated. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		if servers := w.AppServers(h); servers != 1 {
			t.Errorf("app-server servers launched = %d, want the one that ran through the restart", servers)
		}
	})
}

func TestAppServerSessionsKeepTheirAnswersAndLearnRunsThatEndedWhileTheDaemonWasDown(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		t.Setenv("ATTN_PTY_BACKEND", "worker")
		t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
		w := newWorld(t, h)
		t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
		app := w.App()
		asked, askedTUI, server := appServerSession(t, w, app, h)
		app.TypeLine(asked, "which index should the report use?")
		askedTUI.Prompted()
		server.Prompted(askedTUI.ConversationID)
		server.Reply(askedTUI.ConversationID, "By date or by customer? <!-- attn:state=waiting_input -->")
		testworld.AwaitSession(app, asked, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
		working := w.Spawn(app, h, w.Path("shop"))
		workingTUI := w.Launched(working)
		promptUntilWorking(app, working, workingTUI, server, "migrate the orders table")

		w.stop()
		server.Reply(workingTUI.ConversationID, "Migrated, but two rows failed. Retry them? <!-- attn:state=waiting_input -->")
		w.start()
		registerSigmaDriver(t, w, w.App())
		app = w.App()
		testworld.AwaitSessionFromConnect(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
		if got := restartedSession(w.App(), asked); got.State != protocol.SessionStateWaitingInput {
			t.Errorf("the session waiting on an answer is %s after the restart, want it still waiting", got.State)
		}
	})
}

func TestAnAppServerConversationOpenInAnotherProfileIsRefusedThere(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		registerSigmaDriver(t, w, app)
		side := createProfile(app, "Side")
		sideApp := w.AppOn(side.ID)
		sideTUI := w.Launched(w.Spawn(sideApp, h, w.Path("shop")))

		refused, _, _ := w.RequestSpawn(app, h, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
			m.ResumeSessionID = protocol.Ptr(sideTUI.ConversationID)
		})
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), side.ID) {
			t.Errorf("resuming %s here while profile %s holds it = %+v, want it refused naming that profile", sideTUI.ConversationID, side.ID, refused)
		}
	})
}

func TestACommandAnAppServerAgentRunsActsAsItsSession(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		for _, outerCodex := range []bool{false, true} {
			name := "current environment"
			if outerCodex {
				name = "outer shared Codex"
			}
			t.Run(name, func(t *testing.T) {
				if outerCodex {
					t.Setenv("ATTN_CODEX_SHARED_PROFILE", "outer-profile")
					t.Setenv("CODEX_THREAD_ID", "outer-conversation")
				}
				w := newWorld(t, h)
				app := w.App()
				session, nisse, server := appServerSession(t, w, app, h)
				got := strings.TrimSpace(server.RunTool(nisse.ConversationID, `"$ATTN_WRAPPER_PATH" presence`))
				t.Logf("attn presence from the app-server agent: %s", got)
				if got != "running inside attn (session "+session+")" {
					t.Errorf("attn presence from the app-server agent = %q, want session %s", got, session)
				}
				server.RunTool(nisse.ConversationID, `"$ATTN_WRAPPER_PATH" _hook-state idle`)
			})
		}
	})
}

func TestAnAppServerConversationStartsWithAttnsGuidance(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		_, nisse, server := appServerSession(t, w, app, h)
		guidance := server.Started(nisse.ConversationID).Instructions
		if !strings.Contains(guidance, agentGuidanceLead) || !strings.Contains(guidance, "attn pr record") {
			t.Errorf("the app-server conversation started with:\n%s\nwant attn's agent guidance, which asks it to record its own pull requests", guidance)
		}
	})
}

func TestAppServerOffersItsProvidersModelsWithoutKeepingItsServer(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		registerSigmaDriver(t, w, app)
		found := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: string(h), RequestID: "nisse-models"},
			protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "nisse-models" })
		var offered []string
		for _, m := range found.Models {
			offered = append(offered, m.Provider+"/"+m.ID+" "+string(m.EffortSupport)+" "+strings.Join(m.EffortLevels, ","))
		}
		want := []string{"claude/opus supported low,medium,high", "openai/gpt-5.5 supported low,high", "openai/gpt-image unsupported "}
		if !found.Success || !slices.Equal(offered, want) {
			t.Fatalf("app-server's models = %q (%+v), want %q", offered, found, want)
		}
		w.AppServer(h).AwaitStopped()
	})
}

func TestADelegationRoleRunsTheAppServerModelThePickerOffered(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app, cli := w.App(), w.Client()
		registerSigmaDriver(t, w, app)
		roles := buildRole()
		roles.Roles[0].Choices[0].Selection = protocol.DelegationSelection{Harness: string(h), Provider: "openai", Model: "gpt-5.5", Effort: "high"}
		if saved := savePreferences(app, roles); !saved.Success {
			t.Fatalf("saving the build role on app-server's openai/gpt-5.5: %s", protocol.Deref(saved.Error))
		}
		cwd := w.Path("api")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		request := brief(cwd, "rename the checkout module")
		request.Role = protocol.Ptr("build")
		accepted, err := cli.StartDelegation(request)
		if err != nil {
			t.Fatalf("delegating with the build role: %v", err)
		}
		nisse := w.Launched(string(accepted.SessionID))
		if started := w.AppServer(h).Started(nisse.ConversationID); started.Provider != "openai" || started.Model != "gpt-5.5" {
			t.Fatalf("the delegated conversation started as %+v, want openai's gpt-5.5", started)
		}
	})
}

func registerSigmaDriver(t *testing.T, w *world, app *testworld.Peer) {
	t.Helper()
	driver := dialDriver(t, w, "sigma")
	driver.agent = "sigma"
	driver.mu.Lock()
	driver.serverExecutable = filepath.Join(w.Dir, "bin", "sigma")
	driver.mu.Unlock()
	caps := map[string]bool{"server_binding": true, "resume": true, "yolo": true, "initial_prompt": true, "model_pin": true, "effort_pin": true, "launch_instructions": true}
	if err := driver.call("driver.register", map[string]any{"agent": "sigma", "capabilities": caps, "server": serverDriverDeclaration(w)}, &driver.registered); err != nil {
		t.Fatal(err)
	}
	awaitDriverAvailable(app, "sigma")
}
