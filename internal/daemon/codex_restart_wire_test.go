package daemon_test

import (
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
	w.AwaitHeldBoot()
	app.TypeLine(session, "add a discount field")
	codex.Prompted()
	preflight()
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
	w.AwaitHeldBoot()
	awaitSharedCodex(app, func(s *protocol.SharedCodexState) bool {
		return restartState(s, "0.162.1", "0.163.0", protocol.SharedCodexRestartPending) &&
			slices.Equal(s.Blockers, blockedBy(cart, protocol.SharedCodexBlockerReasonStarting))
	})
	boot()
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
