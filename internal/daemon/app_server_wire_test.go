package daemon_test

import (
	"fmt"
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

var appServerHarnesses = []fakeagent.Harness{fakeagent.SampleServer, fakeagent.Sigma}

func eachAppServer(t *testing.T, test func(t *testing.T, h fakeagent.Harness)) {
	for _, h := range appServerHarnesses {
		t.Run(string(h), func(t *testing.T) { test(t, h) })
	}
}

func appServerSession(t *testing.T, w *world, app *testworld.Peer, h fakeagent.Harness, opts ...func(*protocol.SpawnSessionMessage)) (string, *fakeagent.Run, *fakeagent.AppServer) {
	t.Helper()
	offerAppServer(t, w, app, h)
	session := w.Spawn(app, h, w.Path("shop"), opts...)
	return session, w.Launched(session), w.AppServer(h)
}

func promptUntilWorking(app *testworld.Peer, session string, view *fakeagent.Run, server *fakeagent.AppServer, prompt string) {
	app.T.Helper()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	mark := len(app.Log())
	app.TypeLine(session, prompt)
	view.Prompted()
	server.Prompted(view.ConversationID)
	testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func TestAnAppServerSessionRunsItsTUIOnTheConversationAttnNamedAndFollowsTheServersStatus(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h, func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("find the flaky checkout test")
		})
		conversation := view.ConversationID
		if at := slices.Index(view.Argv, "--conversation"); at < 0 || view.Argv[at+1] != conversation || server.Started(conversation).Cwd != w.Path("shop") {
			t.Fatalf("the app-server TUI ran %q on conversation %s, want it to start the conversation attn named in the session's folder", view.Argv, conversation)
		}
		if got := view.Prompted(); got != "find the flaky checkout test" {
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
		if got := view.Prompted(); got != "yes, add it" {
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
		session, view, server := appServerSession(t, w, app, h)
		conversation := view.ConversationID
		app.TypeLine(session, "deploy it")
		view.Prompted()
		server.Prompted(conversation)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.Fail(conversation, "the provider refused the request")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	})
}

func TestAnAppServerRunAnsweredWithoutWordsSettlesOnItsAnswer(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		promptUntilWorking(app, session, view, server, "record the result without a summary")
		mark := len(app.Log())
		server.Reply(view.ConversationID, "")
		testworld.AwaitSessionSince(app, mark, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		stateExplainShows(t, stateExplainOf(t, w.Client(), session).Observations,
			stateExplainRow{source: "classifier", claim: "idle", outcome: "observed", detail: "empty_last_message"},
			stateExplainRow{source: "resolver", claim: "idle", outcome: "applied", cause: "resolver_observation", detail: "classifier_verdict"},
		)
	})
}

func TestFeedbackReachesAnAppServerSessionAsAFollowUpFromAttn(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		conversation := view.ConversationID
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
		session, view, server := appServerSession(t, w, app, h, func(m *protocol.SpawnSessionMessage) {
			m.Model = protocol.Ptr("openai/gpt-5.5")
			m.Effort = protocol.Ptr("high")
			m.YoloMode = protocol.Ptr(true)
		})
		conversation := view.ConversationID
		started := server.Started(conversation)
		if started.Provider != "openai" || started.Model != "gpt-5.5" || started.Effort != "high" || started.Approvals != "auto" {
			t.Fatalf("the conversation started as %+v, want openai's gpt-5.5 at high effort running without asking", started)
		}
		if !view.Yolo {
			t.Errorf("the TUI ran %q, want --auto", view.Argv)
		}
		if closed := closeFromApp(app, session); !closed.Accepted {
			t.Fatalf("close: %s", protocol.Deref(closed.Error))
		}
		view.AwaitGone()

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
		session, view, server := appServerSession(t, w, app, h)
		neighbour := w.Spawn(app, h, w.Path("neighbour"))
		neighbourTUI := w.Launched(neighbour)
		conversation := view.ConversationID
		app.TypeLine(session, "which index should the report use?")
		view.Prompted()
		server.Prompted(conversation)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		server.Reply(conversation, "By date or by customer? <!-- attn:state=waiting_input -->")
		asked := testworld.AwaitSession(app, session, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateWaitingInput && protocol.Deref(s.TurnOwed)
		})

		if closed := closeFromApp(app, session); !closed.Accepted {
			t.Fatalf("close: %s", protocol.Deref(closed.Error))
		}
		view.AwaitGone()
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
		session, view, server := appServerSession(t, w, app, h)
		conversation := view.ConversationID
		promptUntilWorking(app, session, view, server, "migrate the orders table")

		w.restart()
		rejoinAppServer(t, w, h)
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
		rejoinAppServer(t, w, h)
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
		offerAppServer(t, w, app, h)
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
				session, view, server := appServerSession(t, w, app, h)
				got := strings.TrimSpace(server.RunTool(view.ConversationID, `"$ATTN_WRAPPER_PATH" presence`))
				t.Logf("attn presence from the app-server agent: %s", got)
				if got != "running inside attn (session "+session+")" {
					t.Errorf("attn presence from the app-server agent = %q, want session %s", got, session)
				}
				server.RunTool(view.ConversationID, `"$ATTN_WRAPPER_PATH" _hook-state idle`)
			})
		}
	})
}

func TestAnAppServerConversationStartsWithAttnsGuidance(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		_, view, server := appServerSession(t, w, app, h)
		guidance := server.Started(view.ConversationID).Instructions
		if !strings.Contains(guidance, agentGuidanceLead) || !strings.Contains(guidance, "attn pr record") {
			t.Errorf("the app-server conversation started with:\n%s\nwant attn's agent guidance, which asks it to record its own pull requests", guidance)
		}
	})
}

func TestAppServerOffersItsProvidersModelsWithoutKeepingItsServer(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		offerAppServer(t, w, app, h)
		found := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: string(h), RequestID: "server-models"},
			protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "server-models" })
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
		offerAppServer(t, w, app, h)
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
		view := w.Launched(string(accepted.SessionID))
		if started := w.AppServer(h).Started(view.ConversationID); started.Provider != "openai" || started.Model != "gpt-5.5" {
			t.Fatalf("the delegated conversation started as %+v, want openai's gpt-5.5", started)
		}
	})
}

func offerAppServer(t *testing.T, w *world, app *testworld.Peer, h fakeagent.Harness) {
	t.Helper()
	if h == fakeagent.Sigma {
		setSetting(t, app, "sigma_enabled", "true")
		return
	}
	registerSampleServerDriver(t, w, app)
}

func rejoinAppServer(t *testing.T, w *world, h fakeagent.Harness) {
	t.Helper()
	if h != fakeagent.Sigma {
		registerSampleServerDriver(t, w, w.App())
	}
}

func registerSampleServerDriver(t *testing.T, w *world, app *testworld.Peer) {
	t.Helper()
	registerSampleServerDriverDeclaring(t, w, app, serverDriverDeclaration(w))
}

func registerSampleServerDriverDeclaring(t *testing.T, w *world, app *testworld.Peer, declaration map[string]any) {
	t.Helper()
	driver := dialDriver(t, w, "sample-server")
	driver.agent = "sample-server"
	driver.mu.Lock()
	driver.serverExecutable = filepath.Join(w.Dir, "bin", "sample-server")
	driver.mu.Unlock()
	caps := map[string]bool{"server_binding": true, "resume": true, "yolo": true, "initial_prompt": true, "model_pin": true, "effort_pin": true, "launch_instructions": true}
	if err := driver.call("driver.register", map[string]any{"agent": "sample-server", "capabilities": caps, "server": declaration}, &driver.registered); err != nil {
		t.Fatal(err)
	}
	awaitDriverAvailable(app, "sample-server")
}

func TestADelegationToAnAppServerHarnessConfirmsItsFirstTurnOnceTheServerReportsWorking(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		offerAppServer(t, w, w.App(), h)
		cwd := w.Path("api")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		request := brief(cwd, "Fix the flaky checkout test")
		request.Agent = protocol.Ptr(string(h))
		result, err := w.Client().Delegate(request)
		if err != nil {
			t.Fatal(err)
		}
		if unconfirmed := protocol.Deref(result.FirstTurnUnconfirmed); unconfirmed != "" || result.FirstTurnAt == nil {
			t.Errorf("the delegation answered first turn at %v with %q unconfirmed, want its first turn confirmed", protocol.Deref(result.FirstTurnAt), unconfirmed)
		}
		view := w.Launched(string(result.SessionID))
		if prompted := w.AppServer(h).Prompted(view.ConversationID); !strings.Contains(prompted.Text, "Fix the flaky checkout test") {
			t.Errorf("the server was prompted %q, want the brief", prompted.Text)
		}
	})
}

func TestAServerThatAnswersProtocolOneIsRefusedWithTheProcessToStop(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		offerAppServer(t, w, app, h)
		models := func(id string) protocol.HarnessModelsResultMessage {
			return testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: string(h), RequestID: id, Refresh: protocol.Ptr(true)}, protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == id })
		}
		w.AnswerProtocolAtNextBoot(1)
		refused := models("old")
		reason := protocol.Deref(refused.Error)
		if refused.Success {
			t.Fatal("a server answering protocol 1 listed models, want it refused")
		}
		for _, want := range []string{string(h) + " initialize: ", "protocol 1", "only protocol 2", fmt.Sprintf("Stop pid %d", w.AppServer(h).PID()), filepath.Join(w.Dir, "bin", string(h)) + " serve"} {
			if !strings.Contains(reason, want) {
				t.Errorf("the refusal %q does not say %q", reason, want)
			}
		}
		if n := w.AppServers(h); n != 1 {
			t.Errorf("attn started %d servers beside the old one, want none", n-1)
		}
	})
}

func TestAReloadOfAServerSessionWhoseServerAnswersProtocolOneFailsWithTheRefusalAndKeepsItsTerminal(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
		launched := w.AppServers(h)
		w.AnswerProtocolAtNextBoot(1)
		server.Crash()

		reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
			protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
		refusing := w.AppServerAfter(h, launched)
		reason := protocol.Deref(reloaded.Error)
		if reloaded.Success || !strings.Contains(reason, "protocol 1") || !strings.Contains(reason, fmt.Sprintf("Stop pid %d", refusing.PID())) {
			t.Fatalf("reloading against a server answering protocol 1 answered success %t, error %q, want the refusal naming pid %d", reloaded.Success, reason, refusing.PID())
		}
		view.Input("are you still there")
		app.AwaitScreen(session, "are you still there")
	})
}

func serverUsage(tokens int) map[string]any {
	return map[string]any{"input": tokens - 20, "output": 20, "cacheRead": 0, "cacheWrite": 0, "totalTokens": tokens, "cost": map[string]any{"total": 0.01}}
}

func awaitServerFeed(app *testworld.Peer, session, conversation string, server *fakeagent.AppServer, tokens int) *protocol.SessionUsage {
	app.T.Helper()
	server.SetUsage(conversation, "openai/gpt-5.5", serverUsage(tokens))
	return awaitUsageTokens(app, session, tokens)
}

func writeMarkdownCall(path string) fakeagent.ServerToolCall {
	return fakeagent.ServerToolCall{Name: "write", Input: map[string]any{"path": path, "content": "# Plan\n"}, Output: "Successfully wrote to " + path}
}

func recentCount(app *testworld.Peer, path string) int {
	app.T.Helper()
	for _, file := range recentFiles(app, 0, "") {
		if file.Path == path {
			return file.Count
		}
	}
	return 0
}

func TestAHiddenAppServerSessionStillReportsItsRepliesAndPullRequests(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		conversation := view.ConversationID
		promptUntilWorking(app, session, view, server, "open the pull request")

		launched := w.AppServers(h)
		held := w.restartHoldingRevives()
		defer held.Release()
		app, cli := w.App(), w.Client()
		rejoinAppServer(t, w, h)
		server = w.AppServerAfter(h, launched)
		server.ToolCall(conversation, fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "gh pr create --fill"}, Output: shopPull(7) + "\n"})
		server.Reply(conversation, "Opened the pull request.")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateIdle && slices.Equal(pullNumbers(s), []int{7})
		})
		peek, err := cli.AgentPeek(session)
		if err != nil || protocol.Deref(peek.LastAssistantMessage) != "Opened the pull request." {
			t.Fatalf("peek at the hidden session = %q, %v, want its latest reply", protocol.Deref(peek.LastAssistantMessage), err)
		}
		read, err := cli.SessionTranscript(session, "")
		if err != nil || len(read.Events) != 4 {
			t.Fatalf("the hidden session's transcript = %+v, %v, want its prompt, tool call, result and reply", read.Events, err)
		}
	})
}

func TestAnAppServerFeedRecordsEachEntryOnceAcrossDaemonAndServerRestarts(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		conversation, plan := view.ConversationID, markdownFile(t, w, "plan.md")
		promptUntilWorking(app, session, view, server, "write the plan")
		server.ToolCall(conversation, writeMarkdownCall(plan))
		awaitServerFeed(app, session, conversation, server, 100)
		if got := recentCount(app, plan); got != 1 {
			t.Fatalf("plan.md after one edit counts %d, want 1", got)
		}

		launched := w.AppServers(h)
		w.restart()
		app = w.App()
		rejoinAppServer(t, w, h)
		server = w.AppServerAfter(h, launched)
		w.Launched(session)
		server.ToolCall(conversation, writeMarkdownCall(plan))
		awaitServerFeed(app, session, conversation, server, 200)
		if got := recentCount(app, plan); got != 2 {
			t.Fatalf("plan.md after a daemon restart and a second edit counts %d, want 2", got)
		}

		launched = w.AppServers(h)
		server.Crash()
		server = w.AppServerAfter(h, launched)
		server.ToolCall(conversation, writeMarkdownCall(plan))
		awaitServerFeed(app, session, conversation, server, 300)
		if got := recentCount(app, plan); got != 3 {
			t.Fatalf("plan.md after a server restart and a third edit counts %d, want 3", got)
		}
	})
}

func TestAToolThatFinishesAfterADaemonRestartStillRecordsItsFileAndPullRequest(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		conversation, plan := view.ConversationID, markdownFile(t, w, "plan.md")
		promptUntilWorking(app, session, view, server, "write the plan and open its pull request")
		write := writeMarkdownCall(plan)
		pull := fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "gh pr create --fill"}, Output: shopPull(7) + "\n"}
		writing := server.ToolRequested(conversation, write)
		pulling := server.ToolRequested(conversation, pull)
		awaitServerFeed(app, session, conversation, server, 100)

		launched := w.AppServers(h)
		w.restart()
		app = w.App()
		rejoinAppServer(t, w, h)
		server = w.AppServerAfter(h, launched)
		w.Launched(session)
		server.ToolReturned(conversation, writing, write)
		server.ToolReturned(conversation, pulling, pull)
		server.ToolCall(conversation, fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "gh pr create --fill"}, Output: shopPull(8) + "\n"})
		now := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return slices.Contains(pullNumbers(s), 8) })
		if got := pullNumbers(now); !slices.Contains(got, 7) {
			t.Errorf("pull requests %v, want 7, opened by a tool that finished after the restart, beside 8", got)
		}
		if got := recentCount(app, plan); got != 1 {
			t.Fatalf("plan.md, written by a tool that finished after the restart, counts %d, want 1", got)
		}
	})
}

func TestAPluginServerThatDoesNotDeclareConversationWatchGetsNoFeed(t *testing.T) {
	w := newWorld(t, fakeagent.SampleServer)
	app := w.App()
	declaration := serverDriverDeclaration(w)
	declaration["abilities"] = []string{"targets"}
	registerSampleServerDriverDeclaring(t, w, app, declaration)
	session := w.Spawn(app, fakeagent.SampleServer, w.Path("shop"))
	view, server := w.Launched(session), w.AppServer(fakeagent.SampleServer)
	promptUntilWorking(app, session, view, server, "hello")
	server.Reply(view.ConversationID, "hi")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if watchers := server.Watchers(view.ConversationID); watchers != 0 {
		t.Errorf("the undeclared server has %d conversation watches, want none", watchers)
	}
	if _, err := w.Client().SessionTranscript(session, ""); err == nil {
		t.Error("the transcript of a session whose server answers no conversation/watch was readable, want it unavailable")
	}
}
