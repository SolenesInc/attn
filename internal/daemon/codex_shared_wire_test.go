package daemon_test

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
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

func TestSharedCodexSurvivesAnActualDaemonRestartWithTheSameOwnersAndPTYs(t *testing.T) {
	stack := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	stack.Start()
	app := stack.App()
	sharedCodexSetting(t, app, true)
	a := stack.Spawn(app, fakeagent.Codex, stack.Path("exo"))
	agentA := stack.Launched(a)
	awaitSharedView(app, a, a)
	b := stack.Spawn(app, fakeagent.Codex, stack.Path("foo"))
	agentB := stack.Launched(b)
	awaitSharedView(app, b, b)
	app.TypeLine(a, "/agents "+agentB.ConversationID)
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
	w.Launched(a)
	awaitSharedView(app, a, a)
	if err := w.Client().Unregister(a); err != nil {
		t.Fatal(err)
	}
	awaitClosed(app, a)
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

func TestSharedCodexClosingAViewThatNeverInitializedCleansItsUnusedReservation(t *testing.T) {
	t.Setenv("ATTN_FAKE_CODEX_VIEW_FAIL_BEFORE_INITIALIZE", "1")
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	sharedCodexSetting(t, app, true)
	spawn, workspaceID, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("failed"))
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
	if err := w.Client().Unregister(spawn.ID); err != nil {
		t.Fatal(err)
	}
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == workspaceID })
	page, err := w.Client().SessionList(client.SessionListOptions{})
	if err != nil || len(page.Entries) != 0 {
		t.Fatalf("unused failed owner remains: %+v %v", page, err)
	}
}
