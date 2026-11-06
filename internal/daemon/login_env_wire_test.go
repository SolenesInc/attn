package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/supervise"
	"github.com/victorarias/attn/internal/testworld"
)

const loginEnvSetting = "background_login_env_enabled"

type blockedLoginShell struct {
	t        *testing.T
	gate     string
	captures string
}

// installBlockedLoginShell makes a login shell whose capture waits on a fifo once block is called, gives the capture
// alone a variable and a PATH entry, and records each capture.
func installBlockedLoginShell(t *testing.T, w *world) *blockedLoginShell {
	t.Helper()
	shell := &blockedLoginShell{t: t, gate: filepath.Join(w.Dir, "login-gate"), captures: filepath.Join(w.Dir, "login-captures")}
	path := filepath.Join(w.Dir, "login-shell")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$3" = 'env -0' ]; then
  echo capture >> '%s'
  if [ -p '%s' ]; then
    cat '%s' >/dev/null
  fi
  export PATH=/login/bin:$PATH ATTN_TEST_LOGIN_ONLY=present ATTN_CODEX_EXECUTABLE=/no/such/codex
fi
exec /bin/sh "$@"
`, shell.captures, shell.gate, shell.gate)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", path)
	return shell
}

func (s *blockedLoginShell) block() {
	s.t.Helper()
	if err := os.Remove(s.captures); err != nil && !os.IsNotExist(err) {
		s.t.Fatal(err)
	}
	if err := syscall.Mkfifo(s.gate, 0o600); err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(s.release)
}

func (s *blockedLoginShell) release() {
	s.t.Helper()
	gate, err := os.OpenFile(s.gate, os.O_WRONLY, 0)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		s.t.Error(err)
		return
	}
	os.Remove(s.gate)
	gate.Close()
}

func (s *blockedLoginShell) requireOneCapture() {
	s.t.Helper()
	got, err := os.ReadFile(s.captures)
	if err != nil {
		s.t.Fatal(err)
	}
	if n := strings.Count(string(got), "\n"); n != 1 {
		s.t.Fatalf("the login shell ran %d captures, want the daemon's one: a launch that does not wait runs its own", n)
	}
}

func answerBeforeTheCapture(t *testing.T, w *world) {
	t.Helper()
	setSetting(t, w.App(), loginEnvSetting, "true")
}

func TestTheDaemonAnswersWhileTheLoginShellRuns(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	shell := installBlockedLoginShell(t, w)
	report := filepath.Join(t.TempDir(), "plugin-report")
	if err := syscall.Mkfifo(report, 0o600); err != nil {
		t.Fatal(err)
	}
	checkout := pluginsCheckout(t, w.Path("checkouts", "probe"), "probe", "0.1.0",
		"#!/bin/sh\nprintf '%s %s' \"$ATTN_TEST_LOGIN_ONLY\" \"$PATH\" > '"+report+"'\nexec sleep 3600\n")
	w.start()
	answerBeforeTheCapture(t, w)
	pluginsAct(t, w.App(), protocol.InstallPluginMessage{Cmd: protocol.CmdInstallPlugin, Source: checkout}, "install", "probe")
	if _, err := os.ReadFile(report); err != nil {
		t.Fatal(err)
	}

	shell.block()
	w.restart()
	app := w.App()

	if got := app.Initial.Settings["claude_available"]; got != "checking" {
		t.Fatalf("initial_state carries claude_available=%v during the capture, want checking", got)
	}

	requestID := "prefs"
	prefs := testworld.Request(app, protocol.DelegationPreferencesGetMessage{Cmd: protocol.CmdDelegationPreferencesGet, RequestID: requestID},
		protocol.EventDelegationPreferencesResult, func(m protocol.DelegationPreferencesResultMessage) bool { return m.RequestID == requestID })
	for _, harness := range prefs.Harnesses {
		if harness.ID == "claude" && !harness.Available {
			t.Fatal("delegation_preferences_get reports a checking harness as not available")
		}
	}
	app.Send(protocol.ClearWarningsMessage{Cmd: protocol.CmdClearWarnings})
	testworld.Request(app, protocol.SendersGetMessage{Cmd: protocol.CmdSendersGet}, protocol.EventSendersUpdated, func(protocol.SendersUpdatedMessage) bool { return true })

	launch := w.NextLaunch()
	spawned := make(chan protocol.SpawnResultMessage, 1)
	go func() {
		result, _, _ := w.RequestSpawn(app, fakeagent.Claude, w.Path("during-capture"))
		spawned <- result
	}()

	mark := app.EventCount()
	shell.release()

	settled := testworld.AwaitSince(app, mark, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["claude_available"] == "true"
	})
	if got := settled.Settings["codex_available"]; got != "false" {
		t.Fatalf("settings_updated after the capture carries codex_available=%v, want false", got)
	}

	if result := <-spawned; !result.Success {
		t.Fatalf("spawn during the capture: %s", protocol.Deref(result.Error))
	}
	if got, _ := envEntry(launch().Env, "ATTN_TEST_LOGIN_ONLY"); got != "present" {
		t.Fatalf("the agent spawned during the capture has ATTN_TEST_LOGIN_ONLY=%q, want the login environment", got)
	}

	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	loginOnly, path, _ := strings.Cut(string(got), " ")
	if loginOnly != "present" || !slices.Contains(filepath.SplitList(path), "/login/bin") {
		t.Fatalf("the plugin started with %q, want the login environment: ATTN_TEST_LOGIN_ONLY=present and /login/bin on PATH", got)
	}
	shell.requireOneCapture()
}

func TestWithoutTheSettingTheDaemonAnswersAfterTheLoginShell(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	shell := installBlockedLoginShell(t, w)
	w.start()
	w.stop()
	shell.block()
	go shell.release()
	w.start()
	if got := w.App().Initial.Settings["claude_available"]; got != "true" {
		t.Fatalf("initial_state carries claude_available=%v with the setting off, want true", got)
	}
}

func TestAClientWhoseInitialStateRacesTheCaptureEndsUpWithTheAnswer(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	shell := installBlockedLoginShell(t, w)
	w.start()
	answerBeforeTheCapture(t, w)
	shell.block()
	w.stop()
	built := w.PauseAt(pausepoint.DaemonInitialStateBuilt)
	w.start()
	app := w.ConnectApp()
	built.Await()
	settled := func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["claude_available"] == "true" }
	shell.release()
	testworld.Await(app, protocol.EventSettingsUpdated, settled)
	mark := app.EventCount()
	built.Release()

	initial := testworld.AwaitSince[protocol.InitialStateMessage](app, mark, protocol.EventInitialState, nil)
	if got := initial.Settings["claude_available"]; got != "checking" {
		t.Fatalf("the paused initial_state carries claude_available=%v, want the checking it was built with", got)
	}
	testworld.AwaitSince(app, mark, protocol.EventSettingsUpdated, settled)
}

func TestANisseSessionDoesNotHoldTheFirstScreenForTheCapture(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
	w := &world{World: prepareWorld(t, fakeagent.Nisse)}
	t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
	shell := installBlockedLoginShell(t, w)
	w.start()
	answerBeforeTheCapture(t, w)
	app := w.App()
	session, nisse, server := nisseSession(t, w, app)
	conversation := nisse.ConversationID
	app.TypeLine(session, "migrate the orders table")
	nisse.Prompted()
	server.Prompted(conversation)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	shell.block()
	w.restart()
	app = w.App()
	if got := app.Initial.Settings["claude_available"]; got != "checking" {
		t.Fatalf("initial_state carries claude_available=%v during the capture, want checking", got)
	}
	shell.release()

	server.Reply(conversation, "Migrated. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}

func TestADelegationToAPluginHarnessWaitsForItsPluginAcrossARestartDuringTheCapture(t *testing.T) {
	for _, row := range []struct {
		name   string
		choose func(*protocol.DelegateMessage)
	}{
		{name: "named directly", choose: func(request *protocol.DelegateMessage) { request.Agent = protocol.Ptr("plug") }},
		{name: "through a role", choose: func(request *protocol.DelegateMessage) { request.Agent, request.Role = nil, protocol.Ptr("build") }},
	} {
		t.Run(row.name, func(t *testing.T) {
			w := &world{World: prepareWorld(t)}
			shell := installBlockedLoginShell(t, w)
			w.start()
			answerBeforeTheCapture(t, w)
			app := w.App()
			driver := connectDriver(t, w, "attn-plug", "plug", map[string]bool{"state_reporting": true, "initial_prompt": true})
			awaitDriverAvailable(app, "plug")
			onPlug := protocol.DelegationSelection{Harness: "plug"}
			saved := addAttnDelegationRoles(app, protocol.DelegationPreferences{
				Enabled: true, WorkflowSkillEnabled: true,
				Roles: []protocol.DelegationRole{{ID: "build", Name: "Builder", Enabled: true,
					DefaultChoiceID: "default", Choices: []protocol.DelegationChoice{{ID: "default", Name: "Everyday", Selection: onPlug}}}},
				Fallback: protocol.DelegationFallback{Selection: onPlug},
			})
			if !saved.Success {
				t.Fatalf("saving the roles: %s", protocol.Deref(saved.Error))
			}

			repo := newRepo(t, "shop")
			request := delegateCheckoutAt(repo, delegateNewWorktree("feature/plug", "main"))
			request.RequestID, request.Label = "plug-delegation", protocol.Ptr("plug-delegation")
			row.choose(&request)
			driver.mu.Lock()
			driver.holdLaunch = true
			driver.mu.Unlock()
			accepted, err := w.Client().StartDelegation(request)
			if err != nil {
				t.Fatal(err)
			}
			var launching driverLaunch
			driver.asked("driver.spawn", &launching)

			waiting := w.PauseAt(pausepoint.LaunchAwaitsPluginHarness)
			shell.block()
			finishStop := w.beginStop(t)
			driver.hangUp()
			finishStop()
			w.start()
			w.App()
			if _, err := w.Client().StartDelegation(request); err != nil {
				t.Fatalf("the client's retry during the capture: %v", err)
			}
			waiting.Await()
			waiting.Release()
			shell.release()
			driver = connectDriver(t, w, "attn-plug", "plug", map[string]bool{"state_reporting": true, "initial_prompt": true})

			delegated := make(chan *protocol.DelegateResult, 1)
			go func() {
				result, err := w.Client().Delegate(request)
				if err != nil {
					t.Errorf("the delegation after the restart: %v", err)
				}
				delegated <- result
			}()
			if err := driver.state(driver.launched(), 1, "working"); err != nil {
				t.Fatal(err)
			}
			if result := <-delegated; result == nil || result.SessionID != accepted.SessionID {
				t.Fatalf("after the restart the delegation = %+v, want it finished on %s", result, accepted.SessionID)
			}
		})
	}
}

func TestStoppingDuringTheCaptureDoesNotWaitForAChildHoldingTheShellsOutput(t *testing.T) {
	w := &world{World: prepareWorld(t)}
	gate := filepath.Join(w.Dir, "child-gate")
	path := filepath.Join(w.Dir, "login-shell")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = 'env -0' ] && [ -p '%s' ]; then\n  cat '%s' &\n  wait\nfi\nexec /bin/sh \"$@\"\n", gate, gate)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", path)
	w.start()
	answerBeforeTheCapture(t, w)
	w.stop()
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	child := &blockedLoginShell{t: t, gate: gate}
	t.Cleanup(child.release)
	w.start()
	w.App()
	w.stop()
}

func TestALoginShellThatLeavesAChildHoldingItsOutputStillGivesItsEnvironment(t *testing.T) {
	w := &world{World: prepareWorld(t, fakeagent.Claude)}
	gate := filepath.Join(w.Dir, "child-gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	child := &blockedLoginShell{t: t, gate: gate}
	t.Cleanup(child.release)
	path := filepath.Join(w.Dir, "login-shell")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$3\" = 'env -0' ]; then\n  export ATTN_TEST_LOGIN_ONLY=present\n  cat '%s' &\nfi\nexec /bin/sh \"$@\"\n", gate)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", path)
	w.start()
	app := w.App()
	run := w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("shop")))
	if got, _ := envEntry(run.Env, "ATTN_TEST_LOGIN_ONLY"); got != "present" {
		t.Fatalf("the agent has ATTN_TEST_LOGIN_ONLY=%q, want the login environment", got)
	}
}

// restartDuringTheCapture restarts the daemon with its login shell blocked and answers once recovery settled, when a launch that
// finds no harness would fail.
func restartDuringTheCapture(t *testing.T, w *world, shell *blockedLoginShell) (app *testworld.Peer, recovered func()) {
	t.Helper()
	settling := w.PauseAt(pausepoint.RecoverableAgentsReviving)
	shell.block()
	w.restart()
	return w.App(), func() {
		settling.Await()
		settling.Release()
	}
}

func plugWorldBehindALoginShell(t *testing.T) (*world, *blockedLoginShell) {
	t.Helper()
	w := &world{World: prepareWorld(t, fakeagent.Claude, fakeagent.Plug)}
	writeCrewHomes(t, w)
	shell := installBlockedLoginShell(t, w)
	w.start()
	answerBeforeTheCapture(t, w)
	pluginDriverSettings(w.App(), "plug")
	return w, shell
}

func TestANewDelegationToAPluginHarnessDuringTheCaptureWaitsAndRuns(t *testing.T) {
	w, shell := plugWorldBehindALoginShell(t)
	_, recovered := restartDuringTheCapture(t, w, shell)

	request := brief(w.Path("shop"), "Add a discount field")
	if err := os.MkdirAll(request.Cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request.RequestID, request.Agent = "plug-during-capture", protocol.Ptr("plug")
	if _, err := w.Client().StartDelegation(request); err != nil {
		t.Fatal(err)
	}
	recovered()

	delegated := make(chan *protocol.DelegateResult, 1)
	go func() {
		result, err := w.Client().Delegate(request)
		if err != nil {
			t.Errorf("the delegation during the capture: %v", err)
		}
		delegated <- result
	}()
	shell.release()
	result := <-delegated
	if result == nil {
		t.FailNow()
	}
	if got, _ := envEntry(w.Launched(string(result.SessionID)).Env, "ATTN_TEST_LOGIN_ONLY"); got != "present" {
		t.Fatalf("the delegate has ATTN_TEST_LOGIN_ONLY=%q, want the login environment", got)
	}
}

func TestANewDelegationThroughARoleOnAPluginHarnessDuringTheCaptureWaitsAndRuns(t *testing.T) {
	w, shell := plugWorldBehindALoginShell(t)
	onPlug := protocol.DelegationSelection{Harness: "plug"}
	saved := addAttnDelegationRoles(w.App(), protocol.DelegationPreferences{
		Enabled: true, WorkflowSkillEnabled: true,
		Roles: []protocol.DelegationRole{{ID: "build", Name: "Builder", Enabled: true,
			DefaultChoiceID: "default", Choices: []protocol.DelegationChoice{{ID: "default", Name: "Everyday", Selection: onPlug}}}},
		Fallback: protocol.DelegationFallback{Selection: onPlug},
	})
	if !saved.Success {
		t.Fatalf("saving the roles: %s", protocol.Deref(saved.Error))
	}
	_, recovered := restartDuringTheCapture(t, w, shell)
	recovered()

	request := brief(w.Path("shop"), "Add a discount field")
	if err := os.MkdirAll(request.Cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request.Role = protocol.Ptr("build")
	delegated := make(chan *protocol.DelegateResult, 1)
	go func() {
		result, err := w.Client().Delegate(request)
		if err != nil {
			t.Errorf("the delegation during the capture: %v", err)
		}
		delegated <- result
	}()
	shell.release()
	if result := <-delegated; result == nil {
		t.FailNow()
	} else if got, _ := envEntry(w.Launched(string(result.SessionID)).Env, "ATTN_TEST_LOGIN_ONLY"); got != "present" {
		t.Fatalf("the delegate has ATTN_TEST_LOGIN_ONLY=%q, want the login environment", got)
	}
}

func TestMailDuringTheCaptureWakesAMemberOnAPluginHarness(t *testing.T) {
	w, shell := plugWorldBehindALoginShell(t)
	setCrew(t, w.Client(), "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("plug")})

	_, recovered := restartDuringTheCapture(t, w, shell)
	recovered()
	const sender = "mail-sender"
	registerSessions(t, w, w.Client(), sender)

	mailed := make(chan *protocol.AgentMsgResult, 1)
	go func() {
		result, err := w.Client().AgentMsg("keel", protocol.SessionID(sender), "please wake")
		if err != nil {
			t.Errorf("mail during the capture: %v", err)
		}
		mailed <- result
	}()
	shell.release()
	result := <-mailed
	if result == nil {
		t.FailNow()
	}
	if !strings.Contains(result.Detail, "woke Keel") {
		t.Fatalf("mail during the capture = %+v, want Keel woken", result)
	}
	bound := crewRosterMember(t, w.Client(), "keel").BindingSession
	if bound == nil {
		t.Fatalf("mail during the capture = %+v and left Keel asleep", result)
	}
	if got, _ := envEntry(w.Launched(string(*bound)).Env, "ATTN_TEST_LOGIN_ONLY"); got != "present" {
		t.Fatalf("Keel woke with ATTN_TEST_LOGIN_ONLY=%q, want the login environment", got)
	}
}

func TestASpawnOfAPluginHarnessDuringTheCaptureWaitsAndRuns(t *testing.T) {
	w, shell := plugWorldBehindALoginShell(t)
	app, recovered := restartDuringTheCapture(t, w, shell)
	recovered()

	spawned := make(chan protocol.SpawnResultMessage, 1)
	go func() {
		result, _, _ := w.RequestSpawn(app, fakeagent.Plug, w.Path("shop"))
		spawned <- result
	}()
	shell.release()
	result := <-spawned
	if !result.Success {
		t.Fatalf("spawn during the capture: %s", protocol.Deref(result.Error))
	}
	if got, _ := envEntry(w.Launched(string(result.ID)).Env, "ATTN_TEST_LOGIN_ONLY"); got != "present" {
		t.Fatalf("the agent has ATTN_TEST_LOGIN_ONLY=%q, want the login environment", got)
	}
}

func TestAnAgentNoPluginRegistersFailsWithItsErrorOnceThePluginsHadTheirTime(t *testing.T) {
	w, shell := plugWorldBehindALoginShell(t)
	app, recovered := restartDuringTheCapture(t, w, shell)
	recovered()

	const unknown = fakeagent.Harness("no-such-harness")
	want := `agent "no-such-harness" is not available`
	spawned := make(chan protocol.SpawnResultMessage, 1)
	go func() {
		result, _, _ := w.RequestSpawn(app, unknown, w.Path("shop"))
		spawned <- result
	}()
	shell.release()
	w.awaitTimer(daemon.PluginStartLimitTimer)
	select {
	case result := <-spawned:
		t.Fatalf("the spawn answered %+v before the plugins had their time", result)
	default:
	}
	w.advance(supervise.StableConnection)
	if result := <-spawned; result.Success || protocol.Deref(result.Error) != want {
		t.Fatalf("spawn of an unregistered agent = %+v, want %q", result, want)
	}
	if result, _, _ := w.RequestSpawn(app, unknown, w.Path("shop")); result.Success || protocol.Deref(result.Error) != want {
		t.Fatalf("spawn of an unregistered agent after the limit = %+v, want %q at once", result, want)
	}
}

func TestAReloadWaitingForItsPluginHarnessLeavesTheSessionFreeToClose(t *testing.T) {
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", testworld.AttnBinary(t))
	w, shell := plugWorldBehindALoginShell(t)
	t.Cleanup(func() { ptyworker.ReapDataDir(w.Dir) })
	session := w.Spawn(w.App(), fakeagent.Plug, w.Path("shop"))
	w.Launched(session)

	waiting := w.PauseAt(pausepoint.LaunchAwaitsPluginHarness)
	shell.block()
	w.restart()
	app := w.App()
	app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 100, Rows: 30})
	waiting.Await()

	if closed := closeFromApp(app, session); closed.Error != nil {
		t.Fatalf("closing the session while its reload waits: %s", *closed.Error)
	}
	waiting.Release()
	shell.release()
	testworld.Await(app, protocol.EventReloadSessionResult, func(m protocol.ReloadSessionResultMessage) bool { return string(m.ID) == session })
}
