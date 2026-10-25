package main_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASharedCodexKeepsItsAppServerTerminalAndConversationAcrossADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	terminal := app.Terminal(session)
	codex := s.Launched(session)
	conversation := codex.ConversationID
	servers := s.CodexServers()
	if len(servers) != 1 {
		t.Fatalf("app-servers launched = %v, want one", servers)
	}

	s.Stop()
	s.Start()
	app = s.App()
	if got := app.Terminal(session); got != terminal {
		t.Fatalf("after the restart the session shows terminal %s, want %s", got, terminal)
	}
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("codex received %q after the restart", got)
	}
	if codex.ConversationID != conversation {
		t.Fatalf("the terminal shows conversation %s after the restart, want %s", codex.ConversationID, conversation)
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	codex.Reply("Added. Should it round? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
	if got := s.CodexServers(); !slices.Equal(got, servers) {
		t.Errorf("app-servers launched = %v, want the first one, %v, still serving", got, servers)
	}
}

func TestAStoppedSharedCodexSessionResumesWhenItsAppServerListensWhereTheRestartedDaemonDoesNotLook(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	conversation := codex.ConversationID
	codex.Exit(143)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	stranded := s.CodexServer()

	s.Stop()
	stranded.ListenElsewhere()
	s.Start()
	app = s.App()
	stranded.AwaitStopped()
	reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30},
		protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
	if !reloaded.Success {
		t.Fatalf("resume %s: %s", session, protocol.Deref(reloaded.Error))
	}
	resumed := s.Launched(session)
	if !resumed.Resumed || resumed.ConversationID != conversation {
		t.Fatalf("resuming the stopped session ran %q showing %s (resumed=%v), want conversation %s", resumed.Argv, resumed.ConversationID, resumed.Resumed, conversation)
	}
	app.TypeLine(session, "lock the tax lookup")
	if got := resumed.Prompted(); got != "lock the tax lookup" {
		t.Fatalf("the resumed Codex received %q", got)
	}
}

func TestAHiddenSharedCodexTurnSettlesWhenTheRestartedDaemonStopsAnAppServerItCannotReach(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	hidden := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(hidden)
	terminal := app.Terminal(hidden)
	app.TypeLine(hidden, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. Lock it? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, hidden, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
	conversation := codex.ConversationID
	for _, line := range []string{"/new", "add a discount field"} {
		typeLineInto(app, terminal, line)
		codex.Prompted()
	}
	testworld.AwaitSession(app, hidden, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })
	submitFeedback(t, app, hidden, "Lock the tax table before the lookup.")
	stranded := s.CodexServer()
	stranded.Prompted(conversation)
	testworld.AwaitSession(app, hidden, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	codex.Exit(143)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) != hidden })

	s.Stop()
	stranded.ListenElsewhere()
	s.Start()
	app = s.App()
	stranded.AwaitStopped()
	settled := func(x protocol.Session) bool { return protocol.Deref(x.Hidden) && x.State == protocol.SessionStateIdle }
	if i := slices.IndexFunc(app.Initial.Sessions, func(x protocol.Session) bool { return string(x.ID) == hidden }); i < 0 || !settled(app.Initial.Sessions[i]) {
		testworld.AwaitSession(app, hidden, settled)
	}
	if got := s.CodexServers(); len(got) != 1 {
		t.Errorf("app-servers launched = %v, want only the stranded one", got)
	}
}

func TestAnApprovalPendingInASharedCodexIsAnsweredAfterADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	setSetting(t, app, "auto_approve_enabled", "false")
	shareCodex(t, app)
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "run the migration")
	codex.Prompted()
	codex.AskApproval()
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStatePendingApproval })

	s.Stop()
	s.Start()
	app = s.App()
	app.AwaitScreen(session, "Allow the command to run?")
	app.TypeLine(session, "1")
	if got := codex.Answered(); got == "" {
		t.Fatal("the approval took no answer")
	}
	codex.Reply("Migrated. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
}

func TestASharedCodexSessionShownInAnotherSessionsTerminalKeepsItsApprovalMode(t *testing.T) {
	t.Parallel()
	for _, disturbance := range []string{"daemon restart", "reload"} {
		t.Run(disturbance, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
			s.Start()
			app := s.App()
			setSetting(t, app, "auto_approve_enabled", "true")
			shareCodex(t, app)
			unguarded := s.Spawn(app, fakeagent.Codex, s.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.YoloMode = protocol.Ptr(true) })
			yolo := s.Launched(unguarded)
			app.TypeLine(unguarded, "find the flaky checkout test")
			yolo.Prompted()
			yolo.Reply("It races the tax lookup. <!-- attn:state=idle -->")
			testworld.AwaitSession(app, unguarded, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
			conversation := yolo.ConversationID
			first := app.Terminal(unguarded)
			for _, line := range []string{"/new", "add a discount field"} {
				typeLineInto(app, first, line)
				yolo.Prompted()
			}
			testworld.AwaitSession(app, unguarded, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })

			reviewed := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
			codex := s.Launched(reviewed)
			terminal := app.Terminal(reviewed)
			for _, line := range []string{"/resume " + conversation, "lock the tax lookup"} {
				typeLineInto(app, terminal, line)
				codex.Prompted()
			}
			testworld.AwaitSession(app, unguarded, func(x protocol.Session) bool { return !protocol.Deref(x.Hidden) })
			codex.Reply("Locked. <!-- attn:state=idle -->")
			testworld.AwaitSession(app, unguarded, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })

			if disturbance == "daemon restart" {
				s.Stop()
				s.Start()
				app = s.App()
			} else {
				reloaded := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(unguarded), Cols: 100, Rows: 30},
					protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == unguarded })
				if !reloaded.Success {
					t.Fatalf("reload %s: %s", unguarded, protocol.Deref(reloaded.Error))
				}
				codex = s.Launched(unguarded)
			}
			for _, line := range []string{"/new", "write the notes"} {
				typeLineInto(app, terminal, line)
				codex.Prompted()
			}
			testworld.AwaitSession(app, unguarded, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })
			submitFeedback(t, app, unguarded, "Add a regression test.")
			server := s.CodexServer()
			server.Prompted(conversation)
			want := fakeagent.CodexPermissions{ApprovalPolicy: "never", ApprovalsReviewer: "user", Sandbox: "danger-full-access"}
			if got := server.Permissions(conversation); got != want {
				t.Errorf("after the %s attn reloaded YOLO conversation %s with %+v, want %+v", disturbance, conversation, got, want)
			}
		})
	}
}

func TestASharedCodexTerminalOpenedAfterADaemonRestartRunsTheAppServersCodexWhileItIsStillInstalled(t *testing.T) {
	for _, backend := range []string{"worker", "shared host"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
			if backend == "shared host" {
				host := os.Getenv("ATTN_TEST_PTY_HOST")
				if host == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to run the app-server on the shared host")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
			}
			s.Start()
			app := s.App()
			if backend == "shared host" {
				enableSharedHost(t, app)
			}
			codex := s.InstallStandaloneCodex("standalone", "0.162.1")
			setSetting(t, app, "codex_executable", codex)
			shareCodex(t, app)
			spawn := func(project string) (string, *fakeagent.Run) {
				session := s.Spawn(app, fakeagent.Codex, s.Path(project), func(m *protocol.SpawnSessionMessage) {
					m.Executable, m.CodexExecutable = protocol.Ptr(codex), protocol.Ptr(codex)
				})
				return session, s.Launched(session)
			}
			shop, shopCodex := spawn("shop")
			app.TypeLine(shop, "write the release notes")
			shopCodex.Prompted()
			s.InstallStandaloneCodex("standalone", "0.163.0")

			s.Stop()
			s.Start()
			app = s.App()
			running := s.Path("standalone", "releases", "0.162.1", "bin", "codex")
			if _, cart := spawn("cart"); cart.Argv[0] != running {
				t.Errorf("a terminal opened after the restart, while a turn held the app-server on its Codex, ran %s, want the app-server's %s", cart.Argv[0], running)
			}
			s.CodexServer().Reply(shopCodex.ConversationID, "Wrote the release notes.")
			testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
				return m.SharedCodex != nil && protocol.Deref(m.SharedCodex.RunningVersion) == "0.163.0" && m.SharedCodex.Restart == protocol.SharedCodexRestartNone
			})
		})
	}
}

func shareCodex(t *testing.T, app *testworld.Peer) {
	t.Helper()
	requestID := uuid.NewString()
	set := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "codex_shared_enabled", Value: "true", RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(set.Success) {
		t.Fatalf("share Codex: %s", protocol.Deref(set.Error))
	}
}

func TestAHiddenSharedCodexSessionSurvivesADaemonRestartAndTakesInput(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session, conversation := hiddenWaitingSharedCodex(t, s, app)

	s.Stop()
	s.Start()
	app = s.App()
	i := slices.IndexFunc(app.Initial.Sessions, func(x protocol.Session) bool { return string(x.ID) == session })
	if i < 0 {
		t.Fatalf("session %s is gone after the restart", session)
	}
	if hidden := app.Initial.Sessions[i]; !protocol.Deref(hidden.Hidden) || hidden.State != protocol.SessionStateWaitingInput {
		t.Fatalf("after the restart the session is %s (hidden=%v), want it hidden and still waiting", hidden.State, protocol.Deref(hidden.Hidden))
	}
	feedback := "Lock the tax table before the lookup."
	submitFeedback(t, app, session, feedback)
	server := s.CodexServer()
	if got := server.Prompted(conversation); got != feedback {
		t.Fatalf("the hidden conversation took %q, want the feedback", got)
	}
	server.Reply(conversation, "Locked. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
}

func typeLineInto(app *testworld.Peer, terminal, text string) {
	app.T.Helper()
	probe := uuid.NewString()
	testworld.Request(app, protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(terminal), Data: text + "\r", ProbeID: protocol.Ptr(probe)},
		protocol.EventPtyInputProbeResult, func(r protocol.PtyInputProbeResultMessage) bool { return r.ProbeID == probe })
}

func TestAHiddenSharedCodexSessionCutOffByAMachineRestartComesBackIdle(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session, conversation := hiddenWaitingSharedCodex(t, s, app)
	submitFeedback(t, app, session, "Lock the tax table before the lookup.")
	s.CodexServer().Prompted(conversation)
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })

	s.Reboot()
	s.Start()
	app = s.App()
	i := slices.IndexFunc(app.Initial.Sessions, func(x protocol.Session) bool { return string(x.ID) == session })
	if i < 0 {
		t.Fatalf("session %s is gone after the restart", session)
	}
	if back := app.Initial.Sessions[i]; !protocol.Deref(back.Hidden) || back.State != protocol.SessionStateIdle {
		t.Fatalf("after the restart the session is %s (hidden=%v), want it hidden and idle", back.State, protocol.Deref(back.Hidden))
	}
}

func submitFeedback(t *testing.T, app *testworld.Peer, session, text string) {
	t.Helper()
	requestID := uuid.NewString()
	delivered := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{
		Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: protocol.SessionID(session), Text: text,
	}, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == requestID })
	if !delivered.Success {
		t.Fatalf("feedback to session %s: %s", session, protocol.Deref(delivered.Error))
	}
}

func TestAHiddenSharedCodexTurnThatEndsWhileTheDaemonIsDownSettlesAfterTheRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session, conversation := hiddenWaitingSharedCodex(t, s, app)
	submitFeedback(t, app, session, "Lock the tax table before the lookup.")
	server := s.CodexServer()
	server.Prompted(conversation)
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })

	s.Stop()
	server.Halt(conversation)
	s.Start()
	app = s.App()
	i := slices.IndexFunc(app.Initial.Sessions, func(x protocol.Session) bool { return string(x.ID) == session })
	if i < 0 {
		t.Fatalf("session %s is gone after the restart", session)
	}
	if app.Initial.Sessions[i].State != protocol.SessionStateIdle {
		testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	}
}

func TestASharedCodexTurnThatEndsWhileTheDaemonIsDownSettlesAfterTheRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	shareCodex(t, app)
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })

	s.Stop()
	s.CodexServer().Halt(codex.ConversationID)
	s.Start()
	app = s.App()
	i := slices.IndexFunc(app.Initial.Sessions, func(x protocol.Session) bool { return string(x.ID) == session })
	if i < 0 {
		t.Fatalf("session %s is gone after the restart", session)
	}
	if app.Initial.Sessions[i].State != protocol.SessionStateIdle {
		testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	}
}

func hiddenWaitingSharedCodex(t *testing.T, s *testworld.Stack, app *testworld.Peer) (session, conversation string) {
	t.Helper()
	session = s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	terminal := app.Terminal(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. Lock it? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
	conversation = codex.ConversationID
	for _, line := range []string{"/new", "add a discount field"} {
		typeLineInto(app, terminal, line)
		codex.Prompted()
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return protocol.Deref(x.Hidden) })
	return session, conversation
}

func TestRestartNowLetsARunningTurnFinishWhenCodexIsInstalledWithNpm(t *testing.T) {
	for _, backend := range []string{"worker", "shared host"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
			if backend == "shared host" {
				host := os.Getenv("ATTN_TEST_PTY_HOST")
				if host == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to run the app-server on the shared host")
				}
				s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
			}
			s.Start()
			app := s.App()
			if backend == "shared host" {
				enableSharedHost(t, app)
			}
			setSetting(t, app, "codex_executable", s.InstallNpmCodex("npm", "0.162.1"))
			shareCodex(t, app)
			session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
			codex := s.Launched(session)
			app.TypeLine(session, "write the release notes")
			codex.Prompted()
			server := s.CodexServer()

			s.InstallNpmCodex("npm", "0.163.0")
			requestID := uuid.NewString()
			restarted := testworld.Request(app, protocol.RestartSharedCodexMessage{Cmd: protocol.CmdRestartSharedCodex, ProfileID: app.SelectedProfile(), Now: true, RequestID: protocol.Ptr(requestID)},
				protocol.EventRestartSharedCodexResult, func(m protocol.RestartSharedCodexResultMessage) bool { return protocol.Deref(m.RequestID) == requestID })
			if !restarted.Success {
				t.Fatalf("restart now: %s", protocol.Deref(restarted.Error))
			}
			testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
				return m.SharedCodex != nil && m.SharedCodex.Restart == protocol.SharedCodexRestartRestarting && m.SharedCodex.ForceAt != nil
			})
			server.Reply(codex.ConversationID, "Wrote the release notes.")
			testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
				return m.SharedCodex != nil && protocol.Deref(m.SharedCodex.RunningVersion) == "0.163.0" && m.SharedCodex.Restart == protocol.SharedCodexRestartNone
			})
		})
	}
}

func TestACodexUpdateInstalledFromTheAppRunsInATileAndSharedCodexMovesToIt(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Vars = append(s.Vars, "SHELL=/bin/bash")
	s.SetCodexVersion("0.162.1")
	s.SetCodexLatest("0.163.0")
	s.Start()
	app := s.App()
	shareCodex(t, app)
	s.Launched(s.Spawn(app, fakeagent.Codex, s.Path("shop")))
	sharedCodexOn := func(running, installed string) {
		t.Helper()
		testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
			return m.SharedCodex != nil && protocol.Deref(m.SharedCodex.RunningVersion) == running &&
				protocol.Deref(m.SharedCodex.InstalledVersion) == installed && m.SharedCodex.Restart == protocol.SharedCodexRestartNone
		})
	}
	sharedCodexOn("0.162.1", "0.162.1")
	desktop := ""
	for _, profile := range app.Initial.Profiles {
		if profile.ID == app.SelectedProfile() {
			desktop = profile.CurrentDesktopID
		}
	}
	install := func() protocol.InstallCodexUpdateResultMessage {
		t.Helper()
		requestID := uuid.NewString()
		return testworld.Request(app, protocol.InstallCodexUpdateMessage{Cmd: protocol.CmdInstallCodexUpdate, DesktopID: desktop, RequestID: protocol.Ptr(requestID)},
			protocol.EventInstallCodexUpdateResult, func(m protocol.InstallCodexUpdateResultMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	}

	s.FailCodexUpdate("Could not detect how Codex was installed")
	if failed := install(); failed.Success || !strings.Contains(protocol.Deref(failed.Error), "exited 1") {
		t.Fatalf("an update that exits 1 answered %+v, want a failure naming its exit code", failed)
	}
	tile := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.Agent == protocol.SessionAgentShell && e.Session.Label == "Codex update"
	})
	app.AwaitScreen(string(tile.Session.ID), "Could not detect how Codex was installed")
	s.FailCodexUpdate("")
	if installed := install(); !installed.Success || installed.SharedCodex == nil || protocol.Deref(installed.SharedCodex.InstalledVersion) != "0.163.0" {
		t.Fatalf("install the update answered %+v, want success carrying the installed 0.163.0", installed)
	}
	sharedCodexOn("0.163.0", "0.163.0")

	s.SetCodexLatest("0.164.0")
	updated := s.Run(testworld.Invocation{Args: []string{"codex", "update"}})
	if updated.Code != 0 || !strings.Contains(updated.Stdout, "Codex updated to 0.164.0") || !regexp.MustCompile(`(?m)^installed\s+0\.164\.0$`).MatchString(updated.Stdout) {
		t.Fatalf("attn codex update = %+v, want Codex's update output and 0.164.0 installed", updated)
	}
	sharedCodexOn("0.164.0", "0.164.0")
	status := s.Run(testworld.Invocation{Args: []string{"codex", "status"}})
	if status.Code != 0 || !regexp.MustCompile(`(?m)^server\s+0\.164\.0$`).MatchString(status.Stdout) || !regexp.MustCompile(`(?m)^restart\s+none$`).MatchString(status.Stdout) {
		t.Fatalf("attn codex status = %+v, want the server on 0.164.0 and no restart", status)
	}
	if restart := s.Run(testworld.Invocation{Args: []string{"codex", "restart"}}); restart.Code != 0 {
		t.Fatalf("attn codex restart = %+v", restart)
	}
	testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
		return m.SharedCodex != nil && m.SharedCodex.Restart == protocol.SharedCodexRestartRestarting
	})
	sharedCodexOn("0.164.0", "0.164.0")
}

func TestACodexUpdateOfferKeepsItsInstallActionAcrossADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.SetCodexVersion("0.162.1")
	s.SetCodexLatest("0.163.0")
	s.Start()
	offers := func(app *testworld.Peer) []protocol.Notification {
		t.Helper()
		testworld.Await(app, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
			return m.SharedCodex != nil && protocol.Deref(m.SharedCodex.LatestVersion) == "0.163.0" && protocol.Deref(m.SharedCodex.InstalledVersion) == "0.162.1"
		})
		requestID := uuid.NewString()
		listed := testworld.Request(app, protocol.NotificationListMessage{Cmd: protocol.CmdNotificationList, RequestID: protocol.Ptr(requestID)},
			protocol.EventNotificationListResult, func(r protocol.NotificationListResultMessage) bool { return r.RequestID == requestID })
		var found []protocol.Notification
		for _, n := range listed.Notifications {
			if n.Kind == "codex_update_available" {
				found = append(found, n)
			}
		}
		return found
	}
	app := s.App()
	shareCodex(t, app)
	if before := offers(app); len(before) != 1 || len(before[0].Actions) == 0 {
		t.Fatalf("with Codex 0.162.1 installed and 0.163.0 out, the offers are %+v, want one with Install", before)
	}

	s.Stop()
	s.Start()
	for _, connection := range []string{"on restart", "after the version check"} {
		if after := offers(s.App()); len(after) != 1 || len(after[0].Actions) == 0 || after[0].ReadAt != "" {
			t.Errorf("connecting %s, with 0.162.1 still installed, the 0.163.0 offer is %+v, want it unread with Install", connection, after)
		}
	}
}
