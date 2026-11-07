package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func restartSharedCodex(t *testing.T, app *testworld.Peer, now bool) protocol.RestartSharedCodexResultMessage {
	t.Helper()
	requestID := uuid.NewString()
	return testworld.Request(app, protocol.RestartSharedCodexMessage{Cmd: protocol.CmdRestartSharedCodex, ProfileID: app.SelectedProfile(), Now: now, RequestID: protocol.Ptr(requestID)},
		protocol.EventRestartSharedCodexResult, func(m protocol.RestartSharedCodexResultMessage) bool { return protocol.Deref(m.RequestID) == requestID })
}

func restartState(s *protocol.SharedCodexState, running, installed string, restart protocol.SharedCodexRestart) bool {
	return s != nil && protocol.Deref(s.RunningVersion) == running && protocol.Deref(s.InstalledVersion) == installed && s.Restart == restart
}

func sharedCodexOn(t *testing.T, version string) (w *world, app *testworld.Peer, session string, codex *fakeagent.Run) {
	t.Helper()
	w = newWorld(t, fakeagent.Codex)
	w.SetCodexVersion(version)
	app = w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session = w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	return w, app, session, w.Launched(session)
}

func askingWhetherToTrust(w *world, app *testworld.Peer) (session, folder string) {
	app.T.Helper()
	folder = w.Path("untrusted")
	w.LeaveCodexFolderTrustUnanswered(folder)
	session = w.Spawn(app, fakeagent.Codex, folder)
	app.AwaitScreen(session, "Trust this folder?")
	return session, folder
}

func blockedBy(sessionID string, reason protocol.SharedCodexBlockerReason) []protocol.SharedCodexBlocker {
	return []protocol.SharedCodexBlocker{{SessionID: protocol.SessionID(sessionID), Reason: reason}}
}

func TestSharedCodexMovesToTheInstalledCodexOnceNoTurnRunsAndItsTerminalsKeepWhatWasTyped(t *testing.T) {
	w, app, session, codex := sharedCodexOn(t, "0.162.1")
	terminal := app.Terminal(session)
	app.TypeLine(session, "add a discount field")
	codex.Prompted()
	old := w.CodexServer()

	w.SetCodexVersion("0.163.0")
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("cart")))
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartPending) &&
			slices.Equal(s.Blockers, blockedBy(session, protocol.SharedCodexBlockerReasonTurn))
	})
	typeInto(app, terminal, "add a tax field")

	codex.Reply("Added the discount field. <!-- attn:state=idle -->")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone) && len(s.Blockers) == 0
	})
	old.AwaitStopped()
	typeInto(app, terminal, "\r")
	if got := codex.Prompted(); got != "add a tax field" {
		t.Fatalf("after the restart codex took %q, want what was typed before it", got)
	}
}

func TestRestartNowStopsTurnsWaitingOnTheUserAndLetsTheOthersFinish(t *testing.T) {
	w, app, asking, askingCodex := sharedCodexOn(t, "0.162.1")
	app.TypeLine(asking, "migrate the database")
	askingCodex.Prompted()
	askingCodex.AskApproval()
	working := w.Spawn(app, fakeagent.Codex, w.Path("cart"))
	workingCodex := w.Launched(working)
	app.TypeLine(working, "write the release notes")
	workingCodex.Prompted()
	server := w.CodexServer()

	w.SetCodexVersion("0.163.0")
	if result := restartSharedCodex(t, app, true); !result.Success {
		t.Fatalf("restart now: %s", protocol.Deref(result.Error))
	}
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return s != nil && s.Restart == protocol.SharedCodexRestartRestarting && s.ForceAt != nil &&
			slices.Equal(s.Blockers, blockedBy(working, protocol.SharedCodexBlockerReasonTurn))
	})

	server.Reply(workingCodex.ConversationID, "Wrote the release notes.")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone) && s.ForceAt == nil
	})
	server.AwaitStopped()
}

func TestARestartKeepsTheAppServerRunningWhenTheInstalledCodexRefusesToStart(t *testing.T) {
	w, app, session, codex := sharedCodexOn(t, "0.162.1")

	const refusal = `Error: approval_policy = "untrusted" is no longer supported`
	w.SetCodexVersion("0.163.0")
	w.RefuseCodexStart(refusal)
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("cart")))
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartFailed) && protocol.Deref(s.Error) == refusal
	})
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("codex took %q while its app-server kept running", got)
	}
	codex.Reply("Added. <!-- attn:state=idle -->")

	w.RefuseCodexStart("")
	if result := restartSharedCodex(t, app, false); !result.Success {
		t.Fatalf("retry: %s", protocol.Deref(result.Error))
	}
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone) && s.Error == nil
	})
}

func TestASharedCodexAppServerThatExitsBeforeAnsweringIsNotStartedAgainOnEveryConnection(t *testing.T) {
	w, app, _, codex := sharedCodexOn(t, "0.162.1")

	w.RefuseCodexStart(`Error: approval_policy = "untrusted" is no longer supported`)
	codex.CrashAppServer()
	for range 2 {
		result, _, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("cart"))
		if result.Success {
			t.Fatal("a shared Codex terminal launched with no app-server to reach")
		}
		if error := protocol.Deref(result.Error); strings.Contains(error, "starts it again") {
			return
		}
	}
	t.Fatal("every launch started another app-server; want a cooldown named in the error")
}

func TestAQuietRestartWaitsForATerminalAskingWhetherToTrustItsFolder(t *testing.T) {
	w, app, _, _ := sharedCodexOn(t, "0.162.1")
	asking, folder := askingWhetherToTrust(w, app)

	w.SetCodexVersion("0.163.0")
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("cart")))
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartPending) &&
			slices.Equal(s.Blockers, blockedBy(asking, protocol.SharedCodexBlockerReasonStarting))
	})
	app.TypeLine(asking, "")
	w.Launched(asking)
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone)
	})
	if got := w.CodexFolderTrust(folder); got != "trusted" {
		t.Errorf("Codex saved %s as %q after the question was answered, want trusted", folder, got)
	}
	terminal := app.Terminal(asking)
	for _, event := range app.Received() {
		if event.Event == protocol.EventRuntimeRespawned && protocol.Deref(event.ID) == terminal {
			t.Fatal("the restart relaunched the terminal that was asking whether to trust its folder instead of waiting for it")
		}
	}
}

func TestRestartNowRelaunchesATerminalAskingWhetherToTrustItsFolder(t *testing.T) {
	w, app, _, _ := sharedCodexOn(t, "0.162.1")
	asking, folder := askingWhetherToTrust(w, app)

	w.SetCodexVersion("0.163.0")
	if result := restartSharedCodex(t, app, true); !result.Success {
		t.Fatalf("restart now: %s", protocol.Deref(result.Error))
	}
	terminal := app.Terminal(asking)
	testworld.Await(app, protocol.EventRuntimeRespawned, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == terminal })
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone)
	})
	app.TypeLine(asking, "")
	w.Launched(asking)
	if got := w.CodexFolderTrust(folder); got != "trusted" {
		t.Errorf("Codex saved %s as %q after the relaunched terminal's question was answered, want trusted", folder, got)
	}
}

func TestARestartBacksOffWhenATurnStartsWhileItPrepares(t *testing.T) {
	w, app, session, codex := sharedCodexOn(t, "0.162.1")

	w.SetCodexVersion("0.163.0")
	preflight := w.HoldNextBoot()
	if result := restartSharedCodex(t, app, false); !result.Success {
		t.Fatalf("restart: %s", protocol.Deref(result.Error))
	}
	preflight.Await()
	app.TypeLine(session, "add a discount field")
	codex.Prompted()
	preflight.Release()
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartPending) &&
			slices.Equal(s.Blockers, blockedBy(session, protocol.SharedCodexBlockerReasonTurn))
	})

	codex.Reply("Added the discount field. <!-- attn:state=idle -->")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone)
	})
}

func TestATerminalStillLaunchingHoldsTheRestartItsLaunchDiscovered(t *testing.T) {
	w, app, _, _ := sharedCodexOn(t, "0.162.1")

	w.SetCodexVersion("0.163.0")
	boot := w.HoldNextBoot()
	cart := w.Spawn(app, fakeagent.Codex, w.Path("cart"))
	boot.Await()
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartPending) &&
			slices.Equal(s.Blockers, blockedBy(cart, protocol.SharedCodexBlockerReasonStarting))
	})
	boot.Release()
	codex := w.Launched(cart)
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone)
	})
	app.TypeLine(cart, "add a tax field")
	if got := codex.Prompted(); got != "add a tax field" {
		t.Fatalf("the terminal launched during the upgrade took %q after the restart", got)
	}
}

func TestARefusedRestartClearsOnceTheAppServerRunsTheInstalledCodex(t *testing.T) {
	w, app, _, codex := sharedCodexOn(t, "0.162.1")

	w.SetCodexVersion("0.163.0")
	w.RefuseCodexStart(`Error: approval_policy = "untrusted" is no longer supported`)
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("cart")))
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartFailed)
	})

	w.RefuseCodexStart("")
	codex.CrashAppServer()
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone) && s.Error == nil
	})
}

func TestARestartNamesEachSessionOnceByWhatMostNeedsTheUser(t *testing.T) {
	w, app, session, codex := sharedCodexOn(t, "0.162.1")
	app.TypeLine(session, "plan the release")
	codex.Prompted()
	server := w.CodexServer()
	subagent := server.SpawnSubagent(codex.ConversationID)
	server.Work(subagent)
	server.AskQuestion(subagent)

	w.SetCodexVersion("0.163.0")
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("cart")))
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartPending) &&
			slices.Equal(s.Blockers, blockedBy(session, protocol.SharedCodexBlockerReasonQuestion))
	})
}

func TestAQuietRestartWaitsUntilANewAutomationsConversationIsWritten(t *testing.T) {
	w, app, _, _ := sharedCodexOn(t, "0.162.1")
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(w.Path("check"), "trusted")
	applyAutomation(t, w.Client(), manualAutomation(w, "Check the code."))
	awaitAutomationChanged(app, 1)

	arrived, release := w.CodexServer().HoldNextInjectReply()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "while-writing"})
	arrived()
	awaitAutomationRunResult(app, "while-writing")
	w.SetCodexVersion("0.163.0")
	restartSharedCodex(t, app, false)
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartPending) &&
			slices.ContainsFunc(s.Blockers, func(b protocol.SharedCodexBlocker) bool { return b.Reason == protocol.SharedCodexBlockerReasonStarting })
	})

	release("")
	run := awaitOnlyRunSettled(t, w, app)
	if run.State != "delivered" {
		t.Fatalf("the automation whose conversation was being written when the restart came is %s with %q, want delivered", run.State, protocol.Deref(run.LastError))
	}
	w.Launched(string(protocol.Deref(run.SessionID))).Reply("Checked. <!-- attn:state=idle -->")
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.163.0", "0.163.0", protocol.SharedCodexRestartNone)
	})
}

func TestAQueuedRestartWaitsUntilASlashNewConversationIsWritten(t *testing.T) {
	w, app, session, codex := sharedCodexOn(t, "0.162.1")
	terminal := app.Terminal(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	original := codex.ConversationID

	arrived, release := w.CodexServer().HoldNextInjectReply()
	typeInto(app, terminal, "/new\r")
	arrived()
	restartSharedCodex(t, app, false)
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return s.Restart == protocol.SharedCodexRestartPending && slices.Equal(s.Blockers, blockedBy(session, protocol.SharedCodexBlockerReasonStarting))
	})

	mark := app.EventCount()
	release("")
	codex.Prompted()
	testworld.AwaitSince(app, mark, protocol.EventSharedCodexChanged, func(m protocol.SharedCodexChangedMessage) bool {
		return m.SharedCodex.Restart == protocol.SharedCodexRestartNone && len(m.SharedCodex.Blockers) == 0
	})
	typeInto(app, terminal, "add a discount field\r")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("after /new and the restart it waited for, codex took %q", got)
	}
	if codex.ConversationID == original {
		t.Fatalf("after /new and the restart it waited for, the prompt reached the old conversation %s", original)
	}
}

func TestReloadingDuringASlashNewWriteKeepsTheTerminalOnTheConversationItShows(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session, first, terminal := sharedCodexWaiting(t, w, app)
	original := first.ConversationID
	arrived, release := w.CodexServer().HoldNextInjectReply()
	app.TypeLine(session, "/new")
	arrived()

	launchIntentReload(t, app, session)
	reloaded := w.Launched(session)
	if !reloaded.Resumed || reloaded.ConversationID != original {
		t.Fatalf("the reload resumed=%t conversation %q, want the original %q", reloaded.Resumed, reloaded.ConversationID, original)
	}
	release("")

	app.TypeLine(session, "lock the tax lookup")
	if got := reloaded.Prompted(); got != "lock the tax lookup" {
		t.Fatalf("after the reload the conversation took %q", got)
	}
	reloaded.Reply("Locked. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if shown := app.Terminal(session); shown != terminal {
		t.Fatalf("session %s shows in terminal %s after the old write finished, want %s", session, shown, terminal)
	}
	if reloaded.ConversationID != original {
		t.Fatalf("the prompt reached conversation %s, want the original %s", reloaded.ConversationID, original)
	}
}

func sharedCodexAutomation(t *testing.T) (*world, *testworld.Peer) {
	t.Helper()
	w, app, _, _ := sharedCodexOn(t, "0.162.1")
	if err := os.MkdirAll(w.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.SetCodexFolderTrust(w.Path("check"), "trusted")
	applyAutomation(t, w.Client(), manualAutomation(w, "Check the code."))
	awaitAutomationChanged(app, 1)
	return w, app
}

func TestRestartNowRelaunchesAnAutomationStillBootingAndItsCodexTakesThePrompt(t *testing.T) {
	w, app := sharedCodexAutomation(t)
	boot := w.HoldNextBoot()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "booting"})
	boot.Await()
	defer boot.Release()
	answered := awaitAutomationRunResult(app, "booting")
	if answered.Run == nil || answered.Run.State != "pending" {
		t.Fatalf("while its Codex boots the run answered %s, want pending", describeRunAnswer(answered))
	}

	w.SetCodexVersion("0.163.0")
	if result := restartSharedCodex(t, app, true); !result.Success {
		t.Fatalf("restart now: %s", protocol.Deref(result.Error))
	}
	relaunched := w.Launched(string(protocol.Deref(answered.Run.SessionID)))
	if prompt := relaunched.Prompted(); !strings.Contains(prompt, "Check the code.") {
		t.Fatalf("the relaunched Codex took %q, want the automation's prompt", prompt)
	}
	run := awaitOnlyRunSettled(t, w, app)
	if run.State != "delivered" {
		t.Fatalf("the relaunched automation is %s with %q, want delivered", run.State, protocol.Deref(run.LastError))
	}
}

func TestReloadingAnAutomationStillBootingCarriesItsPromptToTheRelaunchedCodex(t *testing.T) {
	w, app := sharedCodexAutomation(t)
	boot := w.HoldNextBoot()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "booting"})
	boot.Await()
	defer boot.Release()
	answered := awaitAutomationRunResult(app, "booting")
	if answered.Run == nil || answered.Run.State != "pending" {
		t.Fatalf("while its Codex boots the run answered %s, want pending", describeRunAnswer(answered))
	}
	session := string(protocol.Deref(answered.Run.SessionID))

	launchIntentReload(t, app, session)
	relaunched := w.Launched(session)
	if prompt := relaunched.Prompted(); !strings.Contains(prompt, "Check the code.") {
		t.Fatalf("the reloaded Codex took %q, want the automation's prompt", prompt)
	}
	run := awaitOnlyRunSettled(t, w, app)
	if run.State != "delivered" {
		t.Fatalf("the reloaded automation is %s with %q, want delivered", run.State, protocol.Deref(run.LastError))
	}
}

func TestRestartNowDuringAnAutomationsOpeningWriteNeverLeavesItPending(t *testing.T) {
	w, app := sharedCodexAutomation(t)
	arrived, _ := w.CodexServer().HoldNextInjectReply()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "writing"})
	arrived()
	awaitAutomationRunResult(app, "writing")

	w.SetCodexVersion("0.163.0")
	if result := restartSharedCodex(t, app, true); !result.Success {
		t.Fatalf("restart now: %s", protocol.Deref(result.Error))
	}
	run := awaitOnlyRunSettled(t, w, app)
	if run.State == "failed" && protocol.Deref(run.LastError) == "" {
		t.Fatalf("the run failed with no reason shown: %+v", run)
	}
}

func TestRestartNowFailsAnAutomationItCouldNotRelaunchAndSaysWhy(t *testing.T) {
	w, app := sharedCodexAutomation(t)
	boot := w.HoldNextBoot()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "booting"})
	boot.Await()
	defer boot.Release()
	awaitAutomationRunResult(app, "booting")
	if err := os.RemoveAll(w.Path("check")); err != nil {
		t.Fatal(err)
	}

	w.SetCodexVersion("0.163.0")
	if result := restartSharedCodex(t, app, true); !result.Success {
		t.Fatalf("restart now: %s", protocol.Deref(result.Error))
	}
	run := awaitOnlyRunSettled(t, w, app)
	t.Logf("settled %s: %s", run.State, protocol.Deref(run.LastError))
	if failure := protocol.Deref(run.LastError); run.State != "failed" || !strings.Contains(failure, "could not relaunch") {
		t.Fatalf("the run is %s with %q, want failed naming the relaunch", run.State, failure)
	}
}
