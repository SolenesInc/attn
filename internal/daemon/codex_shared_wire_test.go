package daemon_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/testworld"
)

func sharedCodexSetting(t *testing.T, app *testworld.Peer, on bool) {
	t.Helper()
	value := "false"
	if on {
		value = "true"
	}
	result := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "codex_shared_enabled", Value: value, RequestID: protocol.Ptr("shared-" + value)}, protocol.EventSettingsUpdated, func(e protocol.SettingsUpdatedMessage) bool {
		return protocol.Deref(e.RequestID) == "shared-"+value
	})
	if !protocol.Deref(result.Success) {
		t.Fatalf("setting shared Codex: %s", protocol.Deref(result.Error))
	}
}

func awaitSharedView(app *testworld.Peer, runtimeID, ownerID string, after ...string) protocol.WorkspaceLayoutPane {
	app.T.Helper()
	var pane protocol.WorkspaceLayoutPane
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, p := range e.WorkspaceLayout.Panes {
			if protocol.Deref(p.RuntimeID) == runtimeID && protocol.Deref(p.SessionID) == ownerID && protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved {
				revision, _ := strconv.ParseUint(protocol.Deref(p.CodexRevision), 10, 64)
				if len(after) > 0 {
					minimum, _ := strconv.ParseUint(after[0], 10, 64)
					if revision <= minimum {
						continue
					}
				}
				pane = p
				return true
			}
		}
		return false
	})
	return pane
}

func TestSharedCodexSwitchesOwnersWithoutReplacingTheTerminalAndAttachesSymmetricViews(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("exo"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("foo"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(a, "/agents "+agentB.ConversationID)
	switched := awaitSharedView(app, a, b)
	reopened, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	attached := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, p := range e.WorkspaceLayout.Panes {
			if protocol.Deref(p.SessionID) == a && protocol.Deref(p.RuntimeID) != a && protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved {
				return true
			}
		}
		return false
	})
	var second protocol.WorkspaceLayoutPane
	for _, p := range attached.WorkspaceLayout.Panes {
		if protocol.Deref(p.SessionID) == a {
			second = p
		}
	}
	if reopened.SessionID != a {
		t.Fatalf("attach created another owner: %+v", reopened)
	}
	app.TypeLine(a, "/agents "+agentA.ConversationID)
	first := awaitSharedView(app, a, a, protocol.Deref(switched.CodexRevision))
	result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: attached.WorkspaceLayout.WorkspaceID, PaneID: second.PaneID}, protocol.CmdWorkspaceLayoutClosePane, attached.WorkspaceLayout.WorkspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	shown, err := cli.SessionShow(a)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("closing one view closed A: %+v %v", shown, err)
	}
	app.TypeLine(a, "still here")
	if got := agentA.Prompted(); got != "still here" {
		t.Fatal(got)
	}
	agentA.Reply("continued <!-- attn:state=idle -->")
	result = workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: attached.WorkspaceLayout.WorkspaceID, PaneID: first.PaneID}, protocol.CmdWorkspaceLayoutClosePane, attached.WorkspaceLayout.WorkspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	awaitClosed(app, a)
	shown, err = cli.SessionShow(b)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("closing A affected B: %+v %v", shown, err)
	}
	reopened, err = cli.SessionReopen(client.SessionReopenOptions{SessionID: a})
	if err != nil || reopened.SessionID != a {
		t.Fatalf("reopen A: %+v %v", reopened, err)
	}
}

func TestSharedCodexNativeNewAndForkKeepTheirModeAfterTheLaunchDefaultIsDisabled(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("same"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	sharedCodexSetting(t, app, false)
	app.TypeLine(a, "/new")
	changed := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, p := range e.WorkspaceLayout.Panes {
			if protocol.Deref(p.RuntimeID) == a && protocol.Deref(p.SessionID) != "" && protocol.Deref(p.SessionID) != a && protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved {
				return true
			}
		}
		return false
	})
	var newID string
	for _, p := range changed.WorkspaceLayout.Panes {
		if protocol.Deref(p.RuntimeID) == a {
			newID = protocol.Deref(p.SessionID)
		}
	}
	agentNew := w.Launched(newID)
	if agentNew.ConversationID == agentA.ConversationID {
		t.Fatal("New reused A")
	}
	old, err := cli.SessionShow(a)
	if err != nil || old.Entry.ClosedAt != nil {
		t.Fatalf("switching to zero views closed A: %+v %v", old, err)
	}
	app.TypeLine(a, "/fork")
	forked := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, p := range e.WorkspaceLayout.Panes {
			if protocol.Deref(p.RuntimeID) == a && protocol.Deref(p.SessionID) != "" && protocol.Deref(p.SessionID) != newID && protocol.Deref(p.SessionID) != a && protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved {
				return true
			}
		}
		return false
	})
	var forkID string
	for _, p := range forked.WorkspaceLayout.Panes {
		if protocol.Deref(p.RuntimeID) == a {
			forkID = protocol.Deref(p.SessionID)
		}
	}
	w.Launched(forkID)
	legacy := w.Spawn(app, fakeagent.Codex, w.Path("legacy"))
	w.Launched(legacy)
	page, err := cli.SessionList(client.SessionListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 4 {
		t.Fatalf("ledger owners = %d, want A/New/fork/legacy", len(page.Entries))
	}
}

func TestSharedCodexTitleAuthorityAndHiddenInputPreserveTheVisibleDraft(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	spawn, workspaceID, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("same"))
	if !spawn.Success {
		t.Fatal(protocol.Deref(spawn.Error))
	}
	a := spawn.ID
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("same"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(a, "/background "+agentB.ConversationID)
	app.AwaitScreen(a, "Background "+agentB.ConversationID)
	layout := testworld.Request(app, protocol.WorkspaceLayoutGetMessage{Cmd: protocol.CmdWorkspaceLayoutGet, WorkspaceID: workspaceID}, protocol.EventWorkspaceLayout, func(e protocol.WorkspaceLayoutMessage) bool { return e.WorkspaceLayout.WorkspaceID == workspaceID })
	for _, p := range layout.WorkspaceLayout.Panes {
		if protocol.Deref(p.RuntimeID) == a && protocol.Deref(p.SessionID) != a {
			t.Fatal("background resume changed foreground")
		}
	}
	app.TypeLine(a, "/cached "+agentB.ConversationID)
	awaitSharedView(app, a, b)
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: "half a thought"})
	app.AwaitScreen(a, "half a thought")
	result := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "hidden-A", SessionID: a, Text: "feedback for hidden A"}, protocol.EventSessionAnnotationsSubmitResult, func(e protocol.SessionAnnotationsSubmitResultMessage) bool { return e.RequestID == "hidden-A" })
	if result.Status != "delivered" {
		t.Fatalf("hidden input: %+v", result)
	}
	if got := agentA.Prompted(); got != "feedback for hidden A" {
		t.Fatal(got)
	}
	agentA.Reply("Hidden reply <!-- attn:state=idle -->")
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: " about B\r"})
	if got := agentB.Prompted(); got != "half a thought about B" {
		t.Fatalf("visible draft changed: %q", got)
	}
	agentB.Reply("B done <!-- attn:state=idle -->")
	app.TypeLine(a, "/title unknown-root")
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, p := range e.WorkspaceLayout.Panes {
			if protocol.Deref(p.RuntimeID) == a {
				return protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionUnresolved && protocol.Deref(p.SessionID) == ""
			}
		}
		return false
	})
	app.TypeLine(a, "/cached "+agentA.ConversationID)
	awaitSharedView(app, a, a)
}

func TestSharedCodexUnansweredInputDoesNotBlockClosingAnotherOwner(t *testing.T) {
	t.Setenv("ATTN_FAKE_CODEX_DROP_TURN_REPLY", "1")
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("exo"))
	agentA := w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("foo"))
	awaitSharedView(app, b, b)
	app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "unanswered-A", SessionID: a, Text: "accepted without a reply"})
	if got := agentA.Prompted(); got != "accepted without a reply" {
		t.Fatal(got)
	}
	if err := w.Client().Unregister(b); err != nil {
		t.Fatal(err)
	}
	awaitClosed(app, b)
	shown, err := w.Client().SessionShow(a)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("unanswered owner was closed: %+v %v", shown, err)
	}
}

func TestSharedCodexSurvivesAnActualDaemonRestartWithTheSameOwnersAndPTYs(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	stack.Start()
	app := stack.App()
	sharedCodexSetting(t, app, true)
	a := stack.Spawn(app, fakeagent.Codex, stack.Path("exo"))
	agentA := stack.Launched(a)
	awaitSharedView(app, a, a)
	app.AwaitScreen(a, "Showing "+agentA.ConversationID)
	b := stack.Spawn(app, fakeagent.Codex, stack.Path("foo"))
	agentB := stack.Launched(b)
	awaitSharedView(app, b, b)
	app.AwaitScreen(b, "Showing "+agentB.ConversationID)
	app.TypeLine(a, "/agents "+agentB.ConversationID)
	app.AwaitScreen(a, "Showing "+agentB.ConversationID)
	awaitSharedView(app, a, b)
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: a, Data: "saved draft"})
	app.AwaitScreen(a, "saved draft")
	stack.Stop()
	stack.Start()
	app = stack.App()
	screen := screenSnapshot(app, a)
	decoded, err := base64.StdEncoding.DecodeString(protocol.Deref(screen.ScreenSnapshot))
	if err != nil || !strings.Contains(string(decoded), "saved draft") {
		t.Fatalf("restart lost draft: %s (%v)", decoded, err)
	}
	result := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "after-restart", SessionID: a, Text: "hidden after restart"}, protocol.EventSessionAnnotationsSubmitResult, func(e protocol.SessionAnnotationsSubmitResultMessage) bool { return e.RequestID == "after-restart" })
	if result.Status != "delivered" {
		t.Fatalf("restart input: %+v", result)
	}
	if got := agentA.Prompted(); got != "hidden after restart" {
		t.Fatal(got)
	}
	shown, err := stack.Client().SessionShow(a)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("restart closed A: %+v %v", shown, err)
	}
	page, err := stack.Client().SessionList(client.SessionListOptions{})
	if err != nil || len(page.Entries) != 2 {
		t.Fatalf("restart duplicated owners: %+v %v", page, err)
	}
}

func TestSharedCodexSteersTheActiveNativeTurnAfterDaemonRestart(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	stack.Start()
	app := stack.App()
	sharedCodexSetting(t, app, true)
	id := stack.Spawn(app, fakeagent.Codex, stack.Path("active-restart"))
	agent := stack.Launched(id)
	awaitSharedView(app, id, id)
	app.TypeLine(id, "keep this native turn active")
	if got := agent.Prompted(); got != "keep this native turn active" {
		t.Fatal(got)
	}
	stack.Stop()
	stack.Start()
	app = stack.App()
	result := testworld.Request(app, protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "active-restart", SessionID: id, Text: "steer the surviving turn"}, protocol.EventSessionAnnotationsSubmitResult, func(e protocol.SessionAnnotationsSubmitResultMessage) bool { return e.RequestID == "active-restart" })
	if result.Status != "delivered" {
		t.Fatalf("active restart delivery: %+v", result)
	}
	if got := agent.Prompted(); got != "steer the surviving turn" {
		t.Fatal(got)
	}
	agent.Reply("turn complete <!-- attn:state=idle -->")
}

func TestSharedCodexInterruptedFinalCloseStaysClosedAndReopensTheSameHistory(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	stack.StartCrashingAt("codex-owner-close-persisted")
	app := stack.App()
	sharedCodexSetting(t, app, true)
	id := stack.Spawn(app, fakeagent.Codex, stack.Path("close-restart"))
	original := stack.Launched(id)
	awaitSharedView(app, id, id)
	app.Send(protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: id, CloseReason: protocol.Ptr("finished before restart")})
	stack.AwaitCrash()
	stack.Start()
	app = stack.App()
	shown, err := stack.Client().SessionShow(id)
	if err != nil || shown.Entry.ClosedAt == nil || protocol.Deref(shown.Entry.CloseReason) != "finished before restart" {
		t.Fatalf("interrupted final close left a live owner: %+v %v", shown, err)
	}
	for _, workspace := range app.Initial.Workspaces {
		if workspace.Layout != nil && slices.ContainsFunc(workspace.Layout.Panes, func(p protocol.WorkspaceLayoutPane) bool { return protocol.Deref(p.RuntimeID) == id }) {
			t.Fatalf("interrupted close retained the old view: %+v", workspace)
		}
	}
	if _, err := stack.Client().SessionReopen(client.SessionReopenOptions{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	updated := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		return slices.ContainsFunc(e.WorkspaceLayout.Panes, func(p protocol.WorkspaceLayoutPane) bool {
			return protocol.Deref(p.SessionID) == id && protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved
		})
	})
	for _, pane := range updated.WorkspaceLayout.Panes {
		if protocol.Deref(pane.SessionID) == id {
			app.AwaitScreen(protocol.Deref(pane.RuntimeID), "Showing "+original.ConversationID)
		}
	}
}

func TestSharedCodexClosingAFailedBlankAttachmentLeavesTheOriginalOwnerLive(t *testing.T) {
	t.Setenv("ATTN_FAKE_CODEX_REJECT_BLANK_ATTACH", "1")
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("blank"))
	original := w.Launched(a)
	awaitSharedView(app, a, a)
	_, err := w.Client().SessionReopen(client.SessionReopenOptions{SessionID: a})
	if err != nil {
		t.Fatal(err)
	}
	failed := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range e.WorkspaceLayout.Panes {
			if protocol.Deref(pane.RuntimeID) != a && protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionDisconnected {
				return true
			}
		}
		return false
	})
	for _, pane := range failed.WorkspaceLayout.Panes {
		if protocol.Deref(pane.RuntimeID) == a {
			continue
		}
		result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: failed.WorkspaceLayout.WorkspaceID, PaneID: pane.PaneID}, protocol.CmdWorkspaceLayoutClosePane, failed.WorkspaceLayout.WorkspaceID)
		if !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
	}
	shown, err := w.Client().SessionShow(a)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("failed attach closed original: %+v %v", shown, err)
	}
	app.TypeLine(a, "original still works")
	if got := original.Prompted(); got != "original still works" {
		t.Fatal(got)
	}
}

func TestSharedCodexUnixUnregisterClosesTheDisplayedOwnerAndRemovesItsWorkspace(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	spawn, workspaceID, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("exo"))
	if !spawn.Success {
		t.Fatal(protocol.Deref(spawn.Error))
	}
	a := spawn.ID
	native := w.Launched(a)
	remoteArg := slices.Index(native.Argv, "--remote")
	if remoteArg < 0 || remoteArg+1 >= len(native.Argv) {
		t.Fatalf("native view has no remote endpoint: %v", native.Argv)
	}
	viewSocket := strings.TrimPrefix(native.Argv[remoteArg+1], "unix://")
	if _, err := os.Stat(viewSocket); err != nil {
		t.Fatal(err)
	}
	awaitSharedView(app, a, a)
	if err := w.Client().Unregister(a); err != nil {
		t.Fatal(err)
	}
	awaitClosed(app, a)
	if _, err := os.Stat(viewSocket); !os.IsNotExist(err) {
		t.Fatalf("closed view socket remains: %s: %v", viewSocket, err)
	}
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == workspaceID })
	shown, err := w.Client().SessionShow(a)
	if err != nil || shown.Entry.ClosedAt == nil {
		t.Fatalf("unregister: %+v %v", shown, err)
	}
	reopened, err := w.Client().SessionReopen(client.SessionReopenOptions{SessionID: a})
	if err != nil || reopened.SessionID != a || protocol.Deref(reopened.PaneID) == "" {
		t.Fatalf("reopen exact pane: %+v %v", reopened, err)
	}
}

func TestSharedCodexInitialCreationRejectionKeepsTheSameOwnerForRetry(t *testing.T) {
	t.Setenv("ATTN_FAKE_CODEX_REJECT_INITIAL_START_ONCE", "1")
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	spawn, _, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("retry"))
	if !spawn.Success {
		t.Fatal(protocol.Deref(spawn.Error))
	}
	app.AwaitScreen(spawn.ID, "fixture initial creation rejected")
	shown, err := cli.SessionShow(spawn.ID)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("rejection removed the launch owner: %+v %v", shown, err)
	}
	app.TypeLine(spawn.ID, "/new")
	native := w.Launched(spawn.ID)
	awaitSharedView(app, spawn.ID, spawn.ID)
	page, err := cli.SessionList(client.SessionListOptions{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != spawn.ID {
		t.Fatalf("retry changed the owner: %+v %v", page, err)
	}
	app.TypeLine(spawn.ID, "retry works")
	if got := native.Prompted(); got != "retry works" {
		t.Fatal(got)
	}
}

func TestSharedCodexClosingAViewThatNeverInitializedCleansItsUnusedReservation(t *testing.T) {
	t.Setenv("ATTN_FAKE_CODEX_VIEW_FAIL_BEFORE_INITIALIZE", "1")
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	spawn, workspaceID, paneID := w.RequestSpawn(app, fakeagent.Codex, w.Path("failed"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	if !spawn.Success {
		t.Fatal(protocol.Deref(spawn.Error))
	}
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range e.WorkspaceLayout.Panes {
			if protocol.Deref(pane.RuntimeID) == spawn.ID {
				return protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionDisconnected
			}
		}
		return false
	})
	result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID}, protocol.CmdWorkspaceLayoutClosePane, workspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == workspaceID })
	page, err := w.Client().SessionList(client.SessionListOptions{})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("unused failed owner remains: %+v %v", page, err)
	}
	sharedCodexSetting(t, app, false)
	next := w.Spawn(app, fakeagent.Codex, w.Path("next-chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	w.Launched(next)
	for _, session := range w.App().Initial.Sessions {
		if session.ID == next && !protocol.Deref(session.ChiefOfStaff) {
			t.Fatal("discarded initial launch kept the Chief role")
		}
	}
}

func TestSharedCodexDetachesAnExtraChiefViewButProtectsTheLastView(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	spawn, workspaceID, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("chief"))
	if !spawn.Success {
		t.Fatal(protocol.Deref(spawn.Error))
	}
	w.Launched(spawn.ID)
	first := awaitSharedView(app, spawn.ID, spawn.ID)
	if result := setChiefOfStaff(app, spawn.ID, true); !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	second, err := w.Client().SessionReopen(client.SessionReopenOptions{SessionID: spawn.ID})
	if err != nil {
		t.Fatal(err)
	}
	attached := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, p := range e.WorkspaceLayout.Panes {
			if p.PaneID == protocol.Deref(second.PaneID) {
				return protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved
			}
		}
		return false
	})
	result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: attached.WorkspaceLayout.WorkspaceID, PaneID: protocol.Deref(second.PaneID)}, protocol.CmdWorkspaceLayoutClosePane, workspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	result = workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: first.PaneID}, protocol.CmdWorkspaceLayoutClosePane, workspaceID)
	if result.Success || !strings.Contains(protocol.Deref(result.Error), "chief of staff is protected") {
		t.Fatalf("last chief view was not protected: %+v", result)
	}
	shown, err := w.Client().SessionShow(spawn.ID)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("protected chief closed: %+v %v", shown, err)
	}
}

func TestSharedCodexFailedReopenPreservesTheOriginalClose(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	cwd := w.Path("reopen")
	a := w.Spawn(app, fakeagent.Codex, cwd)
	w.Launched(a)
	awaitSharedView(app, a, a)
	if err := cli.Unregister(a); err != nil {
		t.Fatal(err)
	}
	awaitClosed(app, a)
	before, err := cli.SessionShow(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cwd); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: a}); err == nil {
		t.Fatal("reopen with missing cwd succeeded")
	}
	after, err := cli.SessionShow(a)
	if err != nil || after.Entry.ClosedAt == nil || protocol.Deref(after.Entry.ClosedAt) != protocol.Deref(before.Entry.ClosedAt) {
		t.Fatalf("failed reopen changed close: %+v %v", after, err)
	}
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	reopened, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: a})
	if err != nil || reopened.SessionID != a {
		t.Fatalf("reopen after rollback: %+v %v", reopened, err)
	}
}

func TestSharedCodexRecreatesADeletedWorktreeAndReopensTheSameNativeHistory(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote=%t", remote), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app, cli := w.App(), w.Client()
			sharedCodexSetting(t, app, true)
			repo := reopenRepoWithOrigin(t)
			worktree := reopenWorktree(t, repo, "feat/shared-reopen")
			cwd := worktree
			id := w.Spawn(app, fakeagent.Codex, cwd)
			original := w.Launched(id)
			awaitSharedView(app, id, id)
			app.TypeLine(id, "save this history")
			original.Prompted()
			original.Reply("saved reply <!-- attn:state=idle -->")
			beforeUsage := testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.Usage != nil && s.Usage.TotalTokens > 0 }).Usage
			closeSession(t, cli, id, "done for now")
			awaitClosed(app, id)
			if remote {
				runGit(t, cwd, "push", "-q", "-u", "origin", "feat/shared-reopen")
			}
			if err := os.RemoveAll(worktree); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "worktree", "prune")
			action := protocol.SessionReopenActionRecreateWorktreeAndReopen
			if remote {
				runGit(t, repo, "branch", "-q", "-D", "feat/shared-reopen")
				action = protocol.SessionReopenActionFetchRecreateAndReopen
			}
			if verdict := reopenVerdict(t, cli, id); !slices.Contains(verdict.Actions, action) {
				t.Fatalf("recreation not offered: %+v", verdict)
			}
			reopened, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: id, Action: string(action)})
			if err != nil {
				t.Fatal(err)
			}
			if reopened.SessionID != id || reopened.Directory != worktree || protocol.Deref(reopened.WorktreeCreated) != worktree {
				t.Fatalf("reopened in wrong place or identity: %+v", reopened)
			}
			pane := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
				return slices.ContainsFunc(e.WorkspaceLayout.Panes, func(p protocol.WorkspaceLayoutPane) bool {
					return protocol.Deref(p.SessionID) == id && protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionResolved
				})
			})
			for _, p := range pane.WorkspaceLayout.Panes {
				if protocol.Deref(p.SessionID) == id {
					app.AwaitScreen(protocol.Deref(p.RuntimeID), "Showing "+original.ConversationID)
				}
			}
			assertLedgerUsage(t, cli, id, beforeUsage)
			if branch := strings.TrimSpace(runGit(t, worktree, "branch", "--show-current")); branch != "feat/shared-reopen" {
				t.Fatalf("restored branch %q", branch)
			}
			page, err := cli.SessionList(client.SessionListOptions{})
			if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != id {
				t.Fatalf("recreation duplicated owners: %+v %v", page, err)
			}
		})
	}
}

func TestSharedCodexWorkspaceClosePreflightsTheProtectedOwner(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	cwd := w.Path("workspace")
	a := w.Spawn(app, fakeagent.Codex, cwd)
	w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, cwd)
	w.Launched(b)
	awaitSharedView(app, b, b)
	if result := setChiefOfStaff(app, b, true); !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: workspaceIDFor(t, w, cwd)}, protocol.EventCommandError, func(e protocol.CommandErrorMessage) bool {
		return protocol.Deref(e.Cmd) == protocol.CmdUnregisterWorkspace
	})
	for _, id := range []string{a, b} {
		shown, err := cli.SessionShow(id)
		if err != nil || shown.Entry.ClosedAt != nil {
			t.Fatalf("failed workspace close affected %s: %+v %v", id, shown, err)
		}
	}
}

func TestSharedCodexDeletedWorktreeArchivesNativeOwnerAndRemovesItsUnresolvedView(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign=%v", foreign), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app, cli := w.App(), w.Client()
			sharedCodexSetting(t, app, true)
			cwd := w.Path("deleted")
			a := w.Spawn(app, fakeagent.Codex, cwd)
			w.Launched(a)
			awaitSharedView(app, a, a)
			workspace := workspaceIDFor(t, w, cwd)
			b := w.Spawn(app, fakeagent.Codex, w.Path("other"))
			agentB := w.Launched(b)
			awaitSharedView(app, b, b)
			if foreign {
				app.TypeLine(a, "/agents "+agentB.ConversationID)
				awaitSharedView(app, a, b)
			} else {
				app.TypeLine(a, "/title unknown")
				testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
					for _, p := range e.WorkspaceLayout.Panes {
						if protocol.Deref(p.RuntimeID) == a {
							return protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionUnresolved
						}
					}
					return false
				})
			}
			app.TypeLine(b, "/title unknown-other")
			testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
				for _, p := range e.WorkspaceLayout.Panes {
					if protocol.Deref(p.RuntimeID) == b {
						return protocol.Deref(p.CodexResolution) == protocol.CodexViewResolutionUnresolved
					}
				}
				return false
			})
			app.TypeLine(b, "/archive-error on")
			app.AwaitScreen(b, "Archive error on")
			if err := os.RemoveAll(cwd); err != nil {
				t.Fatal(err)
			}
			if err := cli.DeleteWorktree(cwd, true); err == nil || !strings.Contains(err.Error(), "retry worktree deletion") || !strings.Contains(err.Error(), a) {
				t.Fatalf("missing partial cleanup error: %v", err)
			}
			shown, err := cli.SessionShow(a)
			if err != nil || shown.Entry.ClosedAt != nil {
				t.Fatalf("failed native archive falsely closed owner: %+v %v", shown, err)
			}
			app.TypeLine(b, "/archive-error off")
			app.AwaitScreen(b, "Archive error off")
			if err := cli.DeleteWorktree(cwd, true); err != nil {
				t.Fatal(err)
			}
			awaitClosed(app, a)
			app.TypeLine(b, "/loaded")
			app.AwaitScreen(b, `Loaded {"data":["`+agentB.ConversationID+`"]}`)
			testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == workspace })
			shown, err = cli.SessionShow(b)
			if err != nil || shown.Entry.ClosedAt != nil {
				t.Fatalf("deletion affected the other owner: %+v %v", shown, err)
			}
			app.TypeLine(b, "B continues after deletion")
			if got := agentB.Prompted(); got != "B continues after deletion" {
				t.Fatal(got)
			}
		})
	}
}

func TestSharedCodexWorkspaceCloseIncludesHiddenOwners(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("hidden-workspace"))
	w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("other-workspace"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(a, "/agents "+agentB.ConversationID)
	awaitSharedView(app, a, b)
	workspace := workspaceIDFor(t, w, w.Path("hidden-workspace"))
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: workspace}, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == workspace })
	awaitClosed(app, a)
	shown, err := cli.SessionShow(b)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("workspace close affected foreign B: %+v %v", shown, err)
	}
	app.TypeLine(b, "B continues")
	if got := agentB.Prompted(); got != "B continues" {
		t.Fatal(got)
	}
}

func TestSharedCodexWorkspaceCloseIncludesMovedOwnerViews(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("moved-source"))
	w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("moved-target"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	source := workspaceIDFor(t, w, w.Path("moved-source"))
	target := workspaceIDFor(t, w, w.Path("moved-target"))
	moved := workspaceLayoutAction(app, protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage{Cmd: protocol.CmdWorkspaceLayoutMoveLeafToWorkspace, SourceWorkspaceID: source, TargetWorkspaceID: target, LeafID: "pane-" + a, AnchorID: protocol.Ptr("pane-" + b), Edge: protocol.WorkspaceLayoutDockEdgeRight}, protocol.CmdWorkspaceLayoutMoveLeafToWorkspace, source)
	if !moved.Success {
		t.Fatal(protocol.Deref(moved.Error))
	}
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		return e.WorkspaceLayout.WorkspaceID == source && len(e.WorkspaceLayout.Panes) == 0
	})
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: source}, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == source })
	awaitClosed(app, a)
	layout := testworld.Request(app, protocol.WorkspaceLayoutGetMessage{Cmd: protocol.CmdWorkspaceLayoutGet, WorkspaceID: target}, protocol.EventWorkspaceLayout, func(e protocol.WorkspaceLayoutMessage) bool { return e.WorkspaceLayout.WorkspaceID == target })
	for _, pane := range layout.WorkspaceLayout.Panes {
		if protocol.Deref(pane.RuntimeID) == a {
			t.Fatal("moved owner left a stale view")
		}
	}
	shown, err := cli.SessionShow(b)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("target owner closed: %+v %v", shown, err)
	}
	app.TypeLine(b, "B continues after source removal")
	if got := agentB.Prompted(); got != "B continues after source removal" {
		t.Fatal(got)
	}
}

func TestSharedCodexAutomationPreservesTheNativeTrustOverride(t *testing.T) {
	t.Setenv("ATTN_FAKE_CODEX_REPORT_VIEW_LAUNCH", "1")
	for _, archived := range []bool{false, true} {
		t.Run(fmt.Sprintf("archived=%t", archived), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			sharedCodexSetting(t, app, true)
			cwd := w.Path("unattended")
			if err := os.MkdirAll(cwd, 0755); err != nil {
				t.Fatal(err)
			}
			applyAutomation(t, w.Client(), fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
id: shared-check
name: Shared check
trigger: {type: manual}
prompt: Check the build.
launch: {driver: codex, model: gpt-5.4, effort: high}
location: {type: directory, path: %q}
`, cwd))
			awaitAutomationChanged(app, "shared-check")
			result := testworld.Request(app, protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: "shared-check", RequestID: "shared-trust"}, protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("shared-trust"))
			if !result.Success {
				t.Fatal(protocol.Deref(result.Error))
			}
			id := protocol.Deref(result.Run.SessionID)
			run := w.Launched(id)
			directory, _ := flagValue(run.Argv, "-C")
			trust := fmt.Sprintf(`projects.%s.trust_level="trusted"`, strconv.Quote(directory))
			if directory == "" || !slices.Contains(run.Argv, trust) || !slices.Contains(run.Argv, "--remote") {
				t.Fatalf("shared unattended launch dropped trust: %q", run.Argv)
			}
			awaitSharedView(app, id, id)
			if archived {
				if err := w.Client().Unregister(id); err != nil {
					t.Fatal(err)
				}
				awaitClosed(app, id)
			}
			if _, err := w.Client().SessionReopen(client.SessionReopenOptions{SessionID: id}); err != nil {
				t.Fatal(err)
			}
			view := w.Launched(id)
			if !slices.Contains(view.Argv, trust) || !slices.Contains(view.Argv, "--remote") {
				t.Fatalf("shared attachment (archived=%t) dropped trust: %q", archived, view.Argv)
			}
		})
	}
}

func TestSharedCodexRejectedInputCanBeExplicitlySubmittedAgain(t *testing.T) {
	t.Setenv("ATTN_FAKE_CODEX_REJECT_INPUT_ONCE", "1")
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	id := w.Spawn(app, fakeagent.Codex, w.Path("exo"))
	native := w.Launched(id)
	awaitSharedView(app, id, id)
	submit := protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "rejected-input", SessionID: id, Text: "try this input"}
	send := func() protocol.SessionAnnotationsSubmitResultMessage {
		return testworld.Request(app, submit, protocol.EventSessionAnnotationsSubmitResult, func(e protocol.SessionAnnotationsSubmitResultMessage) bool { return e.RequestID == submit.RequestID })
	}
	first := send()
	if first.Success || !strings.Contains(protocol.Deref(first.Error), "fixture input rejected") {
		t.Fatalf("explicit rejection: %+v", first)
	}
	second := send()
	if !second.Success || second.Status != "delivered" {
		t.Fatalf("explicit resubmission: %+v", second)
	}
	if got := native.Prompted(); got != submit.Text {
		t.Fatal(got)
	}
}

type heldCodexExit struct {
	*ptybackend.EmbeddedBackend
	mu        sync.Mutex
	runtimeID string
	reached   chan struct{}
	release   chan struct{}
	done      chan struct{}
}

func (b *heldCodexExit) SetExitHandler(handler func(ptybackend.ExitInfo)) {
	b.EmbeddedBackend.SetExitHandler(func(info ptybackend.ExitInfo) {
		b.mu.Lock()
		held := info.ID == b.runtimeID
		b.mu.Unlock()
		if held {
			close(b.reached)
			<-b.release
		}
		handler(info)
		if held {
			close(b.done)
		}
	})
}

func TestSharedCodexRemovedExtraViewDoesNotEmitSessionExit(t *testing.T) {
	backend := &heldCodexExit{EmbeddedBackend: ptybackend.NewEmbedded(nil), reached: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	w := &world{World: prepareWorld(t, fakeagent.Codex), backend: backend}
	w.start()
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	id := w.Spawn(app, fakeagent.Codex, w.Path("exo"))
	w.Launched(id)
	awaitSharedView(app, id, id)
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	var second protocol.WorkspaceLayoutPane
	layout := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range e.WorkspaceLayout.Panes {
			if protocol.Deref(pane.SessionID) == id && protocol.Deref(pane.RuntimeID) != id && protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionResolved {
				second = pane
				return true
			}
		}
		return false
	})
	backend.mu.Lock()
	backend.runtimeID = protocol.Deref(second.RuntimeID)
	backend.mu.Unlock()
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(backend.release) }) })
	result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: layout.WorkspaceLayout.WorkspaceID, PaneID: second.PaneID}, protocol.CmdWorkspaceLayoutClosePane, layout.WorkspaceLayout.WorkspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	<-backend.reached
	release.Do(func() { close(backend.release) })
	<-backend.done
	sharedCodexSetting(t, app, false)
	for _, event := range app.Received() {
		if event.Event == protocol.EventSessionExited && protocol.Deref(event.ID) == protocol.Deref(second.RuntimeID) {
			t.Fatalf("removed extra view emitted owner exit: %+v", event)
		}
	}
	shown, err := cli.SessionShow(id)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("remaining owner: %+v %v", shown, err)
	}
}

func TestSharedCodexNewAfterReopenUsesTheReplacementWorkspace(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	cwd := w.Path("replaced-workspace")
	id := w.Spawn(app, fakeagent.Codex, cwd)
	w.Launched(id)
	awaitSharedView(app, id, id)
	original := workspaceIDFor(t, w, cwd)
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: original}, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == original })
	awaitClosed(app, id)
	reopened, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	var view protocol.WorkspaceLayoutPane
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		if e.WorkspaceLayout.WorkspaceID != reopened.WorkspaceID {
			return false
		}
		for _, pane := range e.WorkspaceLayout.Panes {
			if protocol.Deref(pane.SessionID) == id && protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionResolved {
				view = pane
				return true
			}
		}
		return false
	})
	runtimeID := protocol.Deref(view.RuntimeID)
	app.TypeLine(runtimeID, "/new")
	var successor string
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range e.WorkspaceLayout.Panes {
			if protocol.Deref(pane.RuntimeID) == runtimeID && protocol.Deref(pane.SessionID) != id && protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionResolved {
				successor = protocol.Deref(pane.SessionID)
				return successor != ""
			}
		}
		return false
	})
	w.Launched(successor)
	session := testworld.AwaitSession(app, successor, func(protocol.Session) bool { return true })
	if session.WorkspaceID != reopened.WorkspaceID {
		t.Fatalf("successor workspace %q, wanted replacement %q", session.WorkspaceID, reopened.WorkspaceID)
	}
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: reopened.WorkspaceID}, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == reopened.WorkspaceID })
	awaitClosed(app, successor)
}

func TestSharedCodexInterruptedReservationRemainsClosable(t *testing.T) {
	for _, precreated := range []bool{true, false} {
		t.Run(fmt.Sprintf("precreated-pane-%t", precreated), func(t *testing.T) {
			stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
			stack.StartCrashingAt("codex-launch-reserved")
			app := stack.App()
			sharedCodexSetting(t, app, true)
			cwd := stack.Path("interrupted")
			if err := os.MkdirAll(cwd, 0755); err != nil {
				t.Fatal(err)
			}
			workspace := "workspace-interrupted"
			id := "interrupted-owner"
			testworld.Request(app, protocol.RegisterWorkspaceMessage{Cmd: protocol.CmdRegisterWorkspace, ID: workspace, Directory: cwd, Title: "interrupted"}, protocol.EventWorkspaceRegistered, func(e protocol.WebSocketEvent) bool { return e.Workspace != nil && e.Workspace.ID == workspace })
			if precreated {
				result := workspaceLayoutAction(app, protocol.WorkspaceLayoutAddSessionPaneMessage{Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspace, SessionID: id}, protocol.CmdWorkspaceLayoutAddSessionPane, workspace)
				if !result.Success {
					t.Fatal(protocol.Deref(result.Error))
				}
			}
			app.Send(protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, Agent: "codex", Cwd: cwd, WorkspaceID: workspace, Cols: 80, Rows: 24})
			stack.AwaitCrash()
			stack.Start()
			app = stack.App()
			layout := testworld.Request(app, protocol.WorkspaceLayoutGetMessage{Cmd: protocol.CmdWorkspaceLayoutGet, WorkspaceID: workspace}, protocol.EventWorkspaceLayout, func(e protocol.WorkspaceLayoutMessage) bool { return e.WorkspaceLayout.WorkspaceID == workspace })
			var interrupted protocol.WorkspaceLayoutPane
			for _, pane := range layout.WorkspaceLayout.Panes {
				if protocol.Deref(pane.RuntimeID) == id {
					interrupted = pane
				}
			}
			if interrupted.PaneID == "" || protocol.Deref(interrupted.CodexResolution) != protocol.CodexViewResolutionDisconnected {
				t.Fatalf("interrupted launch lost its close surface: %+v", layout.WorkspaceLayout.Panes)
			}
			result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspace, PaneID: interrupted.PaneID}, protocol.CmdWorkspaceLayoutClosePane, workspace)
			if !result.Success {
				t.Fatal(protocol.Deref(result.Error))
			}
			if _, err := stack.Client().SessionShow(id); err == nil {
				t.Fatal("unused reservation remains after close")
			}
			fresh := stack.App()
			id = stack.Spawn(fresh, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = "interrupted-owner" })
			stack.Launched(id)
			awaitSharedView(fresh, id, id)
		})
	}
}

func TestSharedCodexServerStartFailureRemovesItsReservation(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	id := "failed-shared-start"
	cwd := w.Path("server-failure")
	executable := os.Getenv("ATTN_CODEX_EXECUTABLE")
	t.Setenv("ATTN_CODEX_EXECUTABLE", "/usr/bin/false")
	result, workspace, paneID := w.RequestSpawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = id })
	t.Setenv("ATTN_CODEX_EXECUTABLE", executable)
	if result.Success {
		t.Fatal("failing server executable launched")
	}
	if _, err := w.Client().SessionShow(id); err == nil {
		t.Fatal("failed owner remains open")
	}
	w.restart()
	app = w.App()
	layout := testworld.Request(app, protocol.WorkspaceLayoutGetMessage{Cmd: protocol.CmdWorkspaceLayoutGet, WorkspaceID: workspace}, protocol.EventWorkspaceLayout, func(e protocol.WorkspaceLayoutMessage) bool { return e.WorkspaceLayout.WorkspaceID == workspace })
	for _, pane := range layout.WorkspaceLayout.Panes {
		if pane.PaneID == paneID && protocol.Deref(pane.CodexResolution) != "" {
			t.Fatalf("failed launch left a saved shared view: %+v", pane)
		}
	}
	id = w.Spawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = "failed-shared-start" })
	w.Launched(id)
	awaitSharedView(app, id, id)
}

func TestSharedCodexNativeResumeAfterWorkspaceRemovalRetainsValidPlacementForNew(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("native-reopen-source"))
	original := w.Launched(a)
	awaitSharedView(app, a, a)
	source := workspaceIDFor(t, w, w.Path("native-reopen-source"))
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: source}, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == source })
	awaitClosed(app, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("native-reopen-view"))
	w.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(b, "/agents "+original.ConversationID)
	awaitSharedView(app, b, a)
	listed, err := cli.List("")
	if err != nil {
		t.Fatal(err)
	}
	var reopened protocol.Session
	for _, session := range listed.Sessions {
		if session.ID == a {
			reopened = session
		}
	}
	if reopened.WorkspaceID == "" || !slices.ContainsFunc(listed.Workspaces, func(workspace protocol.Workspace) bool { return workspace.ID == reopened.WorkspaceID }) {
		t.Fatalf("native resume has no valid workspace: %+v", reopened)
	}
	app.TypeLine(b, "/new")
	var successor string
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range e.WorkspaceLayout.Panes {
			if protocol.Deref(pane.RuntimeID) == b && protocol.Deref(pane.SessionID) != a && protocol.Deref(pane.SessionID) != b && protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionResolved {
				successor = protocol.Deref(pane.SessionID)
				return successor != ""
			}
		}
		return false
	})
	w.Launched(successor)
	next := testworld.AwaitSession(app, successor, func(protocol.Session) bool { return true })
	if next.WorkspaceID != reopened.WorkspaceID {
		t.Fatalf("native New lost resumed placement: %+v", next)
	}
	shown, err := cli.SessionShow(a)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("native resume did not reopen original owner: %+v %v", shown, err)
	}
}

func TestSharedCodexFailedWorkspaceCloseRemovesAlreadyClosedPanes(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	sharedCodexSetting(t, app, true)
	cwd := w.Path("partial-workspace-close")
	a := w.Spawn(app, fakeagent.Codex, cwd)
	w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, cwd)
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(b, "/archive-error on "+agentB.ConversationID)
	app.AwaitScreen(b, "Archive error on "+agentB.ConversationID)
	workspace := workspaceIDFor(t, w, cwd)
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: workspace}, protocol.EventCommandError, func(e protocol.CommandErrorMessage) bool {
		return protocol.Deref(e.Cmd) == protocol.CmdUnregisterWorkspace
	})
	shown, err := cli.SessionShow(a)
	if err != nil || shown.Entry.ClosedAt == nil {
		t.Fatalf("first owner was not closed: %+v %v", shown, err)
	}
	layout := testworld.Request(app, protocol.WorkspaceLayoutGetMessage{Cmd: protocol.CmdWorkspaceLayoutGet, WorkspaceID: workspace}, protocol.EventWorkspaceLayout, func(e protocol.WorkspaceLayoutMessage) bool { return e.WorkspaceLayout.WorkspaceID == workspace })
	if slices.ContainsFunc(layout.WorkspaceLayout.Panes, func(p protocol.WorkspaceLayoutPane) bool { return protocol.Deref(p.RuntimeID) == a }) {
		t.Fatalf("failed close retained a removed runtime pane: %+v", layout.WorkspaceLayout)
	}
	app.TypeLine(b, "/archive-error off "+agentB.ConversationID)
	app.AwaitScreen(b, "Archive error off "+agentB.ConversationID)
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: workspace}, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == workspace })
	awaitClosed(app, b)
}

func TestSharedCodexInterruptedAttachmentKeepsAClosablePane(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	stack.Start()
	app := stack.App()
	sharedCodexSetting(t, app, true)
	id := stack.Spawn(app, fakeagent.Codex, stack.Path("attach-crash"))
	stack.Launched(id)
	awaitSharedView(app, id, id)
	stack.Stop()
	stack.StartCrashingAt("codex-attach-view-persisted")
	app = stack.App()
	app.Send(protocol.SessionReopenMessage{Cmd: protocol.CmdSessionReopen, SessionID: id})
	stack.AwaitCrash()
	stack.Start()
	app = stack.App()
	var interrupted protocol.WorkspaceLayoutPane
	var workspaceID string
	for _, workspace := range app.Initial.Workspaces {
		if workspace.Layout == nil {
			continue
		}
		for _, pane := range workspace.Layout.Panes {
			if protocol.Deref(pane.RuntimeID) != id && protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionDisconnected {
				interrupted, workspaceID = pane, workspace.ID
			}
		}
	}
	if interrupted.PaneID == "" {
		t.Fatal("interrupted attachment has no pane to inspect or close")
	}
	result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: interrupted.PaneID}, protocol.CmdWorkspaceLayoutClosePane, workspaceID)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	shown, err := stack.Client().SessionShow(id)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("closing interrupted attachment closed live owner: %+v %v", shown, err)
	}
}

func TestSharedCodexInterruptedExtraViewCloseDoesNotLeaveADeadPane(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	stack.Start()
	app := stack.App()
	sharedCodexSetting(t, app, true)
	id := stack.Spawn(app, fakeagent.Codex, stack.Path("extra-close-crash"))
	stack.Launched(id)
	awaitSharedView(app, id, id)
	attached, err := stack.Client().SessionReopen(client.SessionReopenOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	var extra protocol.WorkspaceLayoutPane
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		for _, pane := range e.WorkspaceLayout.Panes {
			if pane.PaneID == protocol.Deref(attached.PaneID) && protocol.Deref(pane.CodexResolution) == protocol.CodexViewResolutionResolved {
				extra = pane
				return true
			}
		}
		return false
	})
	stack.Stop()
	stack.StartCrashingAt("codex-view-removed")
	app = stack.App()
	app.Send(protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: attached.WorkspaceID, PaneID: extra.PaneID})
	stack.AwaitCrash()
	stack.Start()
	app = stack.App()
	for _, workspace := range app.Initial.Workspaces {
		if workspace.Layout != nil && slices.ContainsFunc(workspace.Layout.Panes, func(p protocol.WorkspaceLayoutPane) bool { return p.PaneID == extra.PaneID }) {
			t.Fatalf("closed extra view retained a dead pane: %+v", workspace.Layout)
		}
	}
	shown, err := stack.Client().SessionShow(id)
	if err != nil || shown.Entry.ClosedAt != nil {
		t.Fatalf("extra close closed owner: %+v %v", shown, err)
	}
}

func TestSharedCodexLastPaneClosePublishesEmptyLayoutWithHiddenOwner(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	a := w.Spawn(app, fakeagent.Codex, w.Path("hidden-after-pane-close"))
	w.Launched(a)
	awaitSharedView(app, a, a)
	b := w.Spawn(app, fakeagent.Codex, w.Path("other-live-view"))
	agentB := w.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(a, "/agents "+agentB.ConversationID)
	pane := awaitSharedView(app, a, b)
	workspace := workspaceIDFor(t, w, w.Path("hidden-after-pane-close"))
	result := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspace, PaneID: pane.PaneID}, protocol.CmdWorkspaceLayoutClosePane, workspace)
	if !result.Success {
		t.Fatal(protocol.Deref(result.Error))
	}
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(e protocol.WorkspaceLayoutUpdatedMessage) bool {
		return e.WorkspaceLayout.WorkspaceID == workspace && len(e.WorkspaceLayout.Panes) == 0
	})
	for _, id := range []string{a, b} {
		shown, err := w.Client().SessionShow(id)
		if err != nil || shown.Entry.ClosedAt != nil {
			t.Fatalf("pane close closed owner %s: %+v %v", id, shown, err)
		}
	}
}
