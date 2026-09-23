package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func TestSessionProtocolLifecycleMatchesAppOrder(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sessionID := "session-shell-1"
	cwd := t.TempDir()
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        sessionID,
		Label:     protocol.Ptr("shell"),
		Cwd:       cwd,
		Agent:     protocol.AgentShellValue,
		ProfileID: profile.ID,
		Placement: &protocol.SessionPlacement{},
		Cols:      80,
		Rows:      24,
	})
	expectSpawnResult(t, client, sessionID, true)
	placement, placed, err := d.store.SessionPlacement(sessionID)
	if err != nil || !placed || placement.DesktopID != profile.CurrentDesktopID {
		t.Fatalf("placement = %+v placed=%v err=%v, want the profile's current desktop", placement, placed, err)
	}
	desktop, err := d.store.GetDesktop(placement.DesktopID)
	if err != nil || desktop.ActivePaneID != placement.PaneID || desktop.Panes[0].Status != profiles.PaneStatusReady || desktop.Panes[0].Title != "shell" {
		t.Fatalf("desktop = %+v err=%v, want the new agent's ready pane focused", desktop, err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: sessionID})
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after its close", sessionID)
	}
	if _, placed, _ := d.store.SessionPlacement(sessionID); placed {
		t.Fatalf("session %s kept its pane after its close", sessionID)
	}
	if desktop, err := d.store.GetDesktop(placement.DesktopID); err != nil || len(desktop.Panes) != 0 {
		t.Fatalf("desktop after close = %+v err=%v, want it empty and kept", desktop, err)
	}
}

func TestWorkspaceLayoutCloseFinalPanePreservesPinnedWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-pinned"
	sessionID := "session-pinned"
	paneID := "pane-pinned"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "Pinned", Directory: cwd,
	})
	if _, errMsg := d.setWorkspacePinned(workspaceID, true); errMsg != "" {
		t.Fatalf("pin workspace: %s", errMsg)
	}
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspaceID,
		PaneID: protocol.Ptr(paneID), SessionID: sessionID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: sessionID, Cwd: cwd, Agent: protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store), Cols: 80, Rows: 24,
	})
	expectSpawnResult(t, client, sessionID, true)

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)

	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session still registered after closing its pane: %+v", session)
	}
	if layout := d.store.GetWorkspaceLayout(workspaceID); layout != nil {
		t.Fatalf("empty workspace layout survived close: %+v", layout)
	}
	workspace := d.store.GetWorkspace(workspaceID)
	if workspace == nil || !workspace.Pinned {
		t.Fatalf("pinned workspace was removed after closing its final pane: %+v", workspace)
	}
}

func TestWorkspaceLayoutClosePaneKeepsVisibleStateWhenTeardownPreparationFails(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-close-failure"
	sessionID := "session-close-failure"
	paneID := "pane-close-failure"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "Close failure", Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspaceID,
		PaneID: protocol.Ptr(paneID), SessionID: sessionID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: sessionID, Label: "closing", Agent: protocol.SessionAgentCodex, Directory: cwd,
		WorkspaceID: workspaceID, State: protocol.SessionStateWorking,
		ProfileID:  defaultProfileID(t, d.store),
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.prepareSessionTeardownHook = func(string) error { return errors.New("tombstone write failed") }

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, false)
	if d.store.Get(sessionID) == nil {
		t.Fatal("failed close removed the session")
	}
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot == nil || !layouttree.HasPane(snapshot.Layout, paneID) {
		t.Fatalf("failed close removed the pane: %+v", snapshot)
	}
	if d.hasForcedStopMark(sessionID) {
		t.Fatal("failed close left a forced-stop classification mark")
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.killed) != 0 {
		t.Fatalf("failed close killed sessions: %v", backend.killed)
	}
}

func TestWorkspaceLayoutCloseFailedPlaceholderDoesNotCreateTeardown(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-failed-placeholder"
	sessionID := "session-failed-placeholder"
	paneID := "pane-failed-placeholder"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "Failed placeholder", Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspaceID,
		PaneID: protocol.Ptr(paneID), SessionID: sessionID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.setWorkspacePaneStatusForSession(sessionID, workspacelayout.PaneStatusFailed, "launch failed")

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil {
		t.Fatalf("failed placeholder layout survived close: %+v", snapshot)
	}
	if workspace := d.store.GetWorkspace(workspaceID); workspace != nil {
		t.Fatalf("empty workspace survived failed placeholder close: %+v", workspace)
	}
	if err := d.store.AddCheckedUnlessTeardown(&protocol.Session{ID: sessionID, Label: "retry"}); err != nil {
		t.Fatalf("closing failed placeholder blocked session retry: %v", err)
	}
}

func TestSessionProtocolSpawnFailureCreatesNoPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newWorkspaceProtocolTestClient()
	sessionID := "session-bare-spawn-fails"
	cwd := t.TempDir()

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        sessionID,
		Label:     protocol.Ptr("bare shell"),
		Cwd:       cwd,
		Agent:     protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store),
		Placement: &protocol.SessionPlacement{},
		Cols:      80,
		Rows:      24,
	})
	expectSpawnResult(t, client, sessionID, false)

	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("failed bare spawn registered session %s", sessionID)
	}
	if _, placed, _ := d.store.SessionPlacement(sessionID); placed {
		t.Fatalf("failed spawn left a ghost pane for session %s", sessionID)
	}
}

func TestWorkspaceLayoutAddSessionPaneCorrelatesSetupFailure(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	paneID := "pane-requested"

	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: "workspace-missing",
		PaneID:      protocol.Ptr(paneID),
		SessionID:   "session-requested",
	})

	expectWorkspaceLayoutActionResult(
		t,
		client,
		protocol.CmdWorkspaceLayoutAddSessionPane,
		"workspace-missing",
		paneID,
		false,
	)
}

func TestWorkspaceSessionProtocolShellSpawnsIdleNotWorking(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-shell-idle"
	sessionID := "session-shell-idle"
	paneID := "pane-session-shell-idle"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Shell Idle",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr("shell"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        sessionID,
		Label:     protocol.Ptr("shell"),
		Cwd:       cwd,
		Agent:     protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store),
		Cols:      80,
		Rows:      24,
	})
	expectSpawnResult(t, client, sessionID, true)

	session := d.store.Get(sessionID)
	if session == nil {
		t.Fatalf("session %s was not registered", sessionID)
	}
	if session.State != protocol.SessionStateIdle {
		t.Fatalf("shell session state = %q, want %q", session.State, protocol.SessionStateIdle)
	}
	ws, ok := d.workspaces.snapshot(workspaceID)
	if !ok {
		t.Fatalf("workspace %s missing from registry", workspaceID)
	}
	if ws.Status != protocol.WorkspaceStatusIdle {
		t.Fatalf("workspace rollup status = %q, want %q", ws.Status, protocol.WorkspaceStatusIdle)
	}
}

func TestWorkspaceLayoutClosePanePersistsRemovalBeforeSessionUnregistered(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-close-order"
	sessionID := "session-close-order"
	paneID := "pane-session-close-order"
	cwd := t.TempDir()

	d.ptyBackend = &fakeSpawnBackend{}

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Close Order",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr("shell"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:       protocol.CmdSpawnSession,
		ID:        sessionID,
		Label:     protocol.Ptr("shell"),
		Cwd:       cwd,
		Agent:     protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store),
		Cols:      80,
		Rows:      24,
	})
	expectSpawnResult(t, client, sessionID, true)

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutClosePane,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after close", sessionID)
	}
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil {
		t.Fatalf("workspace layout still exists after closing only pane: %+v", snapshot)
	}
	if workspace := d.store.GetWorkspace(workspaceID); workspace != nil {
		t.Fatalf("workspace still exists after closing its only session pane: %+v", workspace)
	}
}

func TestWorkspaceLayoutClosePaneRepliesAndBroadcastsBeforeStubbornPTYExits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := ptybackend.NewEmbedded(pty.NewManager(nil))
	d.ptyBackend = backend
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })

	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-stubborn-close"
	sessionID := "session-stubborn-close"
	paneID := "pane-stubborn-close"
	remainingSessionID := "session-remaining"
	remainingPaneID := "pane-remaining"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "Stubborn Close", Directory: cwd,
	})
	for _, pane := range []struct{ paneID, sessionID string }{
		{paneID, sessionID},
		{remainingPaneID, remainingSessionID},
	} {
		d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
			Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: workspaceID,
			PaneID: protocol.Ptr(pane.paneID), SessionID: pane.sessionID,
		})
		expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, pane.paneID, true)
		d.store.Add(&protocol.Session{
			ID: pane.sessionID, Label: pane.sessionID, Agent: protocol.SessionAgentShell,
			Directory: cwd, WorkspaceID: workspaceID, ProfileID: defaultProfileID(t, d.store),
		})
		d.associateSessionWithWorkspace(pane.sessionID, workspaceID)
	}

	if err := backend.Spawn(context.Background(), ptybackend.SpawnOptions{
		ID: sessionID, CWD: cwd, Agent: "probe-close", Cols: 80, Rows: 24,
		ExternalCommand: []string{"/bin/bash", "-c", `trap '' TERM HUP; read -r _; printf '__CLOSE_READY__\n'; while :; do read -r -t 1 _ || :; done`},
	}); err != nil {
		t.Fatalf("spawn stubborn PTY: %v", err)
	}
	_, stream, err := backend.Attach(context.Background(), sessionID, "close-order-test")
	if err != nil {
		t.Fatalf("attach stubborn PTY: %v", err)
	}
	defer stream.Close()
	if err := backend.Input(context.Background(), sessionID, []byte("\n")); err != nil {
		t.Fatalf("release stubborn PTY readiness gate: %v", err)
	}
	waitForPTYOutput(t, stream, "__CLOSE_READY__")

	layoutBroadcast := make(chan *protocol.WorkspaceLayout, 1)
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		if event.Event == protocol.EventWorkspaceLayoutUpdated && event.WorkspaceLayout != nil {
			layoutBroadcast <- event.WorkspaceLayout
		}
	}

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)

	select {
	case layout := <-layoutBroadcast:
		for _, pane := range layout.Panes {
			if pane.PaneID == paneID {
				t.Fatalf("layout broadcast still contains closed pane %s: %+v", paneID, layout)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pane-free layout broadcast")
	}

	info, err := backend.SessionInfo(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("session info before teardown completed: %v", err)
	}
	if !info.Running {
		t.Fatal("stubborn PTY exited before the close result and layout broadcast arrived")
	}
	if err := backend.Kill(context.Background(), sessionID, syscall.SIGKILL); err != nil {
		t.Fatalf("finish stubborn PTY teardown: %v", err)
	}
}

func waitForPTYOutput(t *testing.T, stream ptybackend.Stream, marker string) {
	t.Helper()
	var output []byte
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-stream.Events():
			output = append(output, event.Data...)
			if bytes.Contains(output, []byte(marker)) {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for PTY marker %q; output=%q", marker, output)
		}
	}
}

func TestWorkspaceLayoutStartupReconcileRemovesOrphanButKeepsUnresolvedPanes(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	cwd := t.TempDir()

	for _, fixture := range []struct {
		workspaceID string
		sessionID   string
		status      workspacelayout.PaneStatus
	}{
		{workspaceID: "workspace-orphan", sessionID: "session-gone", status: workspacelayout.PaneStatusReady},
		{workspaceID: "workspace-pending", sessionID: "session-pending", status: workspacelayout.PaneStatusSpawning},
		{workspaceID: "workspace-failed", sessionID: "session-failed", status: workspacelayout.PaneStatusFailed},
	} {
		d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
			Cmd: protocol.CmdRegisterWorkspace, ID: fixture.workspaceID, Title: fixture.workspaceID, Directory: cwd,
		})
		d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
			Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: fixture.workspaceID,
			PaneID: protocol.Ptr("pane-" + fixture.sessionID), SessionID: fixture.sessionID,
		})
		expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, fixture.workspaceID, "pane-"+fixture.sessionID, true)
		snapshot := d.store.GetWorkspaceLayout(fixture.workspaceID)
		snapshot.Panes[0].Status = fixture.status
		if err := d.store.SaveWorkspaceLayout(*snapshot); err != nil {
			t.Fatalf("save %s fixture: %v", fixture.workspaceID, err)
		}
	}

	d.reconcileWorkspaceLayoutsWithPTYBackend(context.Background())

	if orphan := d.store.GetWorkspaceLayout("workspace-orphan"); orphan != nil {
		t.Fatalf("orphan layout survived startup reconciliation: %+v", orphan)
	}
	if pending := d.store.GetWorkspaceLayout("workspace-pending"); pending == nil || !layouttree.HasPane(pending.Layout, "pane-session-pending") {
		t.Fatalf("valid pending spawn was removed: %+v", pending)
	}
	if failed := d.store.GetWorkspaceLayout("workspace-failed"); failed == nil || !layouttree.HasPane(failed.Layout, "pane-session-failed") {
		t.Fatalf("failed pane was removed: %+v", failed)
	}
}

func TestWorkspaceSessionProtocolRespawnFailureRestoresExistingSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-respawn-fails"
	originalDirectory := t.TempDir()
	requestedDirectory := t.TempDir()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: workspaceID, Title: "Respawn", Directory: originalDirectory,
	})
	original := &protocol.Session{
		ID: "existing-session", Label: "preserved", Agent: protocol.SessionAgentShell,
		Directory: originalDirectory, WorkspaceID: workspaceID, ProfileID: defaultProfileID(t, d.store), State: protocol.SessionStateIdle,
		StateSince: "before", StateUpdatedAt: "before", LastSeen: "before",
	}
	if err := d.store.AddChecked(original); err != nil {
		t.Fatal(err)
	}

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: original.ID, Label: protocol.Ptr("replacement"),
		Cwd: requestedDirectory, Agent: protocol.AgentShellValue, ProfileID: defaultProfileID(t, d.store), Cols: 80, Rows: 24,
	})
	expectSpawnResult(t, client, original.ID, false)
	got := d.store.Get(original.ID)
	if got == nil || got.Directory != originalDirectory || got.Label != original.Label || got.LastSeen != original.LastSeen {
		t.Fatalf("session after failed respawn = %+v, want restored %+v", got, original)
	}
}

func TestWorkspaceLayoutSplitPaneCommandIsUnsupported(t *testing.T) {
	if _, _, err := protocol.ParseMessage([]byte(`{"cmd":"workspace_layout_split_pane","workspace_id":"ws","target_pane_id":"pane","direction":"vertical"}`)); err == nil {
		t.Fatal("legacy workspace_layout_split_pane command parsed successfully")
	}
}

func TestWorkspaceLayoutSetSplitRatioPersistsLockedRatio(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-split-ratio"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Split Ratio",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-1"),
		SessionID:   "session-1",
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-1", true)

	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:          protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID:  workspaceID,
		PaneID:       protocol.Ptr("pane-2"),
		SessionID:    "session-2",
		TargetPaneID: protocol.Ptr("pane-1"),
		Direction:    protocol.Ptr(protocol.LayoutSplitDirectionVertical),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-2", true)

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after adding two panes")
	}
	splitID := firstSplitID(snapshot.Layout)
	if splitID == "" {
		t.Fatalf("expected a split in layout, got %+v", snapshot.Layout)
	}

	d.handleWorkspaceLayoutSetSplitRatio(client, &protocol.WorkspaceLayoutSetSplitRatioMessage{
		Cmd:         protocol.CmdWorkspaceLayoutSetSplitRatio,
		WorkspaceID: workspaceID,
		SplitID:     "does-not-exist",
		Ratio:       0.3,
		RequestID:   protocol.Ptr("request-missing"),
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutSetSplitRatio, workspaceID, "", "does-not-exist", "", "request-missing", false)

	d.handleWorkspaceLayoutSetSplitRatio(client, &protocol.WorkspaceLayoutSetSplitRatioMessage{
		Cmd:         protocol.CmdWorkspaceLayoutSetSplitRatio,
		WorkspaceID: workspaceID,
		SplitID:     splitID,
		Ratio:       0.3,
		RequestID:   protocol.Ptr("request-real"),
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutSetSplitRatio, workspaceID, "", splitID, "", "request-real", true)

	reread, err := d.ensureWorkspaceLayout(workspaceID)
	if err != nil {
		t.Fatalf("ensureWorkspaceLayout: %v", err)
	}
	split := findSplit(reread.Layout, splitID)
	if split == nil {
		t.Fatalf("split %s missing after set ratio: %+v", splitID, reread.Layout)
	}
	if !split.RatioLocked {
		t.Fatalf("split should be locked after set ratio")
	}
	if split.RatioMode != layouttree.RatioModePreferred {
		t.Fatalf("split ratio mode = %q, want preferred", split.RatioMode)
	}
	if split.Ratio < 0.29 || split.Ratio > 0.31 {
		t.Fatalf("split ratio = %v, want ~0.3 (locked ratio must not be rebalanced)", split.Ratio)
	}
}

func TestWorkspaceLayoutDockTilePersistsAndMoves(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-dock-tile"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Dock Tile",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-1"),
		SessionID:   "session-1",
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-1", true)
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:          protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID:  workspaceID,
		PaneID:       protocol.Ptr("pane-2"),
		SessionID:    "session-2",
		TargetPaneID: protocol.Ptr("pane-1"),
		Direction:    protocol.Ptr(protocol.LayoutSplitDirectionVertical),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-2", true)

	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-1",
		Edge:         protocol.LayoutDockEdgeRight,
		TileID:       "pane-2",
		TileKind:     "markdown",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "pane-2", false)
	afterCollision := d.store.GetWorkspaceLayout(workspaceID)
	if !layouttree.HasPane(afterCollision.Layout, "pane-2") || layouttree.HasTile(afterCollision.Layout, "pane-2") {
		t.Fatalf("pane id collision mutated layout: %+v", afterCollision.Layout)
	}

	if err := d.dockTile(workspaceID, "pane-1", "tile-md", "markdown", "/tmp/notes.md", "", protocol.LayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after docking tile")
	}
	if !layouttree.HasTile(snapshot.Layout, "tile-md") {
		t.Fatalf("tile not present after dock: %+v", snapshot.Layout)
	}
	if ids := layouttree.PaneIDs(snapshot.Layout); len(ids) != 2 {
		t.Fatalf("pane ids = %v, want the two agent panes only", ids)
	}
	if len(snapshot.Panes) != 2 {
		t.Fatalf("snapshot panes = %+v, want only the two agent panes", snapshot.Panes)
	}

	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:          protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID:  workspaceID,
		PaneID:       protocol.Ptr("tile-md"),
		SessionID:    "session-3",
		TargetPaneID: protocol.Ptr("pane-1"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "tile-md", false)
	afterPaneCollision := d.store.GetWorkspaceLayout(workspaceID)
	if len(afterPaneCollision.Panes) != 2 || !layouttree.HasTile(afterPaneCollision.Layout, "tile-md") {
		t.Fatalf("tile id collision mutated layout: %+v", afterPaneCollision)
	}

	encoded, err := layouttree.EncodeLayout(snapshot.Layout)
	if err != nil {
		t.Fatalf("EncodeLayout: %v", err)
	}
	decoded, err := layouttree.DecodeLayout(encoded)
	if err != nil {
		t.Fatalf("DecodeLayout: %v", err)
	}
	if !layouttree.HasTile(decoded, "tile-md") {
		t.Fatal("tile lost across layout JSON reload")
	}

	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-2",
		Edge:         protocol.LayoutDockEdgeBottom,
		TileID:       "tile-md",
		TileKind:     "markdown",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "tile-md", true)
	moved := d.store.GetWorkspaceLayout(workspaceID)
	if ids := layouttree.TileIDs(moved.Layout); len(ids) != 1 {
		t.Fatalf("tile ids after move = %v, want exactly one", ids)
	}
	if params, ok := layouttree.TileParamsByID(moved.Layout, "tile-md"); !ok || params != "/tmp/notes.md" {
		t.Fatalf("tile params after move = (%q, %v), want (%q, true)", params, ok, "/tmp/notes.md")
	}

	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-md",
		TileParams:  "/tmp/updated.md",
		RequestID:   "request-reject-markdown-update",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-md", "request-reject-markdown-update", false)
	unchanged := d.store.GetWorkspaceLayout(workspaceID)
	if params, ok := layouttree.TileParamsByID(unchanged.Layout, "tile-md"); !ok || params != "/tmp/notes.md" {
		t.Fatalf("markdown tile params = (%q, %v), want (%q, true)", params, ok, "/tmp/notes.md")
	}

	d.ensureGardenCollections()
	seedSchema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	for _, seed := range []garden.Seed{
		{ID: "s-0jd001", Title: "Original seed", Status: garden.StatusPlanted},
		{ID: "s-new002", Title: "Child seed", Status: garden.StatusPlanted},
	} {
		if _, err := d.plantSeed(*seedSchema, seed); err != nil {
			t.Fatalf("plant seed %s: %v", seed.ID, err)
		}
	}
	if err := d.dockTile(workspaceID, "pane-1", "tile-seed", string(layouttree.TileKindSeed), "s-0jd001", "", protocol.LayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dock seed tile: %v", err)
	}
	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-seed",
		TileParams:  "s-new002",
		RequestID:   "request-update-seed",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-seed", "request-update-seed", true)
	seedUpdated := d.store.GetWorkspaceLayout(workspaceID)
	if params, ok := layouttree.TileParamsByID(seedUpdated.Layout, "tile-seed"); !ok || params != "s-new002" {
		t.Fatalf("seed tile params = (%q, %v), want (%q, true)", params, ok, "s-new002")
	}
	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-seed",
		TileParams:  "s-gone03",
		RequestID:   "request-reject-missing-seed",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-seed", "request-reject-missing-seed", false)
	if params, ok := layouttree.TileParamsByID(d.store.GetWorkspaceLayout(workspaceID).Layout, "tile-seed"); !ok || params != "s-new002" {
		t.Fatalf("missing seed retarget changed params = (%q, %v), want (%q, true)", params, ok, "s-new002")
	}

	d.store.Add(&protocol.Session{ID: "session-2", Label: "Two", WorkspaceID: workspaceID, ProfileID: defaultProfileID(t, d.store), Directory: cwd})
	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:           protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID:   workspaceID,
		TileID:        "tile-md",
		TileParams:    "/tmp/notes.md",
		TileSessionID: protocol.Ptr("session-unknown"),
		RequestID:     "request-retarget-unknown-session",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-md", "request-retarget-unknown-session", false)
	if sessionID, ok := layouttree.TileSessionIDByID(d.store.GetWorkspaceLayout(workspaceID).Layout, "tile-md"); ok && sessionID == "session-unknown" {
		t.Fatalf("dangling session binding persisted: %q", sessionID)
	}

	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:           protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID:   workspaceID,
		TileID:        "tile-md",
		TileParams:    "/tmp/notes.md",
		TileSessionID: protocol.Ptr("session-2"),
		RequestID:     "request-retarget-markdown",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-md", "request-retarget-markdown", true)
	retargeted := d.store.GetWorkspaceLayout(workspaceID)
	if sessionID, ok := layouttree.TileSessionIDByID(retargeted.Layout, "tile-md"); !ok || sessionID != "session-2" {
		t.Fatalf("markdown tile session = (%q, %v), want (%q, true)", sessionID, ok, "session-2")
	}
	if params, ok := layouttree.TileParamsByID(retargeted.Layout, "tile-md"); !ok || params != "/tmp/notes.md" {
		t.Fatalf("markdown tile params after retarget = (%q, %v), want unchanged %q", params, ok, "/tmp/notes.md")
	}

	if err := d.dockTile(workspaceID, "pane-2", "tile-browser", "browser", "https://example.com/", "", protocol.LayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dock browser tile: %v", err)
	}
	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-browser",
		TileParams:  "https://example.com/docs",
		RequestID:   "request-update-browser",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-browser", "request-update-browser", true)
	updated := d.store.GetWorkspaceLayout(workspaceID)
	if params, ok := layouttree.TileParamsByID(updated.Layout, "tile-browser"); !ok || params != "https://example.com/docs" {
		t.Fatalf("browser tile params = (%q, %v), want (%q, true)", params, ok, "https://example.com/docs")
	}
	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:           protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID:   workspaceID,
		TileID:        "tile-browser",
		TileParams:    "https://example.com/combined",
		TileSessionID: protocol.Ptr("session-2"),
		RequestID:     "request-retarget-and-update-browser",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-browser", "request-retarget-and-update-browser", true)
	combined := d.store.GetWorkspaceLayout(workspaceID)
	if sessionID, ok := layouttree.TileSessionIDByID(combined.Layout, "tile-browser"); !ok || sessionID != "session-2" {
		t.Fatalf("browser tile session after combined update = (%q, %v), want (%q, true) — params save clobbered the rebind", sessionID, ok, "session-2")
	}
	if params, ok := layouttree.TileParamsByID(combined.Layout, "tile-browser"); !ok || params != "https://example.com/combined" {
		t.Fatalf("browser tile params after combined update = (%q, %v), want (%q, true)", params, ok, "https://example.com/combined")
	}

	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-browser",
		TileParams:  "file:///tmp/private.txt",
		RequestID:   "request-reject-browser-file-url",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-browser", "request-reject-browser-file-url", false)
	afterRejectedURL := d.store.GetWorkspaceLayout(workspaceID)
	if params, ok := layouttree.TileParamsByID(afterRejectedURL.Layout, "tile-browser"); !ok || params != "https://example.com/combined" {
		t.Fatalf("browser tile params after rejected URL = (%q, %v), want (%q, true)", params, ok, "https://example.com/combined")
	}

	if err := d.dockTile(workspaceID, "pane-1", "tile-notebook", string(layouttree.TileKindNotebook), "", "", protocol.LayoutDockEdgeLeft, nil); err != nil {
		t.Fatalf("dock notebook tile: %v", err)
	}
	if params, ok := layouttree.TileParamsByID(d.store.GetWorkspaceLayout(workspaceID).Layout, "tile-notebook"); !ok || params != "" {
		t.Fatalf("fresh notebook tile params = (%q, %v), want empty", params, ok)
	}
	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-notebook",
		TileParams:  "/notes/knowledge/decisions.md",
		RequestID:   "request-update-notebook",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-notebook", "request-update-notebook", true)
	if params, ok := layouttree.TileParamsByID(d.store.GetWorkspaceLayout(workspaceID).Layout, "tile-notebook"); !ok || params != "/notes/knowledge/decisions.md" {
		t.Fatalf("notebook tile params after update = (%q, %v), want the opened path", params, ok)
	}

	d.handleWorkspaceLayoutUndockTile(client, &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-md",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutUndockTile, workspaceID, "", "", "tile-md", true)
	after := d.store.GetWorkspaceLayout(workspaceID)
	if layouttree.HasTile(after.Layout, "tile-md") {
		t.Fatal("tile still present after undock")
	}
	if ids := layouttree.PaneIDs(after.Layout); len(ids) != 2 {
		t.Fatalf("pane ids after undock = %v, want the two agent panes intact", ids)
	}

	d.handleWorkspaceLayoutUndockTile(client, &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-md",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutUndockTile, workspaceID, "", "", "tile-md", false)
}

func TestWorkspaceLayoutDockTileMessageParamsField(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-dock-tile-params-field"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Dock Tile Params Field",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-1"),
		SessionID:   "session-1",
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-1", true)

	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-1",
		Edge:         protocol.LayoutDockEdgeRight,
		TileID:       "tile-notebook",
		TileKind:     string(layouttree.TileKindNotebook),
		TileParams:   protocol.Ptr("/notes/knowledge/decisions.md"),
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "tile-notebook", true)
	fresh := d.store.GetWorkspaceLayout(workspaceID)
	if params, ok := layouttree.TileParamsByID(fresh.Layout, "tile-notebook"); !ok || params != "/notes/knowledge/decisions.md" {
		t.Fatalf("fresh tile params = (%q, %v), want (%q, true)", params, ok, "/notes/knowledge/decisions.md")
	}

	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-1",
		Edge:         protocol.LayoutDockEdgeBottom,
		TileID:       "tile-notebook",
		TileKind:     string(layouttree.TileKindNotebook),
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "tile-notebook", true)
	moved := d.store.GetWorkspaceLayout(workspaceID)
	if params, ok := layouttree.TileParamsByID(moved.Layout, "tile-notebook"); !ok || params != "/notes/knowledge/decisions.md" {
		t.Fatalf("moved tile params = (%q, %v), want unchanged (%q, true)", params, ok, "/notes/knowledge/decisions.md")
	}
}

func firstSplitID(node layouttree.Node) string {
	if node.Type == "split" {
		return node.SplitID
	}
	for _, child := range node.Children {
		if id := firstSplitID(child); id != "" {
			return id
		}
	}
	return ""
}

func findSplit(node layouttree.Node, splitID string) *layouttree.Node {
	if node.Type == "split" && node.SplitID == splitID {
		found := node
		return &found
	}
	for i := range node.Children {
		if found := findSplit(node.Children[i], splitID); found != nil {
			return found
		}
	}
	return nil
}

func newWorkspaceProtocolTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 32),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func expectWorkspaceLayoutActionResult(t *testing.T, client *wsClient, action, workspaceID, paneID string, success bool) {
	t.Helper()
	expectWorkspaceLayoutActionResultIDs(t, client, action, workspaceID, paneID, "", "", success)
}

func expectWorkspaceLayoutActionResultIDs(t *testing.T, client *wsClient, action, workspaceID, paneID, splitID, tileID string, success bool) {
	t.Helper()
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, action, workspaceID, paneID, splitID, tileID, "", success)
}

func expectWorkspaceLayoutActionResultIDsAndRequestID(t *testing.T, client *wsClient, action, workspaceID, paneID, splitID, tileID, requestID string, success bool) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.WorkspaceLayoutActionResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventWorkspaceLayoutActionResult {
				continue
			}
			if result.Action != action || result.WorkspaceID != workspaceID {
				continue
			}
			if result.Success != success {
				t.Fatalf("workspace action success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			if got := protocol.Deref(result.PaneID); got != paneID {
				t.Fatalf("workspace action pane_id = %q, want %q; payload=%s", got, paneID, string(outbound.payload))
			}
			if got := protocol.Deref(result.SplitID); got != splitID {
				t.Fatalf("workspace action split_id = %q, want %q; payload=%s", got, splitID, string(outbound.payload))
			}
			if got := protocol.Deref(result.TileID); got != tileID {
				t.Fatalf("workspace action tile_id = %q, want %q; payload=%s", got, tileID, string(outbound.payload))
			}
			if got := protocol.Deref(result.RequestID); got != requestID {
				t.Fatalf("workspace action request_id = %q, want %q; payload=%s", got, requestID, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for workspace action %s", action)
		}
	}
}

func expectSpawnResult(t *testing.T, client *wsClient, sessionID string, success bool) protocol.SpawnResultMessage {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.SpawnResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventSpawnResult {
				continue
			}
			if result.ID != sessionID {
				continue
			}
			if result.Success != success {
				t.Fatalf("spawn success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			return result
		case <-deadline:
			t.Fatalf("timed out waiting for spawn_result for %s", sessionID)
		}
	}
}

func expectCommandError(t *testing.T, client *wsClient, cmd, errorContains string) {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var event protocol.WebSocketEvent
			if err := json.Unmarshal(outbound.payload, &event); err != nil || event.Event != protocol.EventCommandError {
				continue
			}
			if protocol.Deref(event.Cmd) != cmd {
				continue
			}
			if !strings.Contains(protocol.Deref(event.Error), errorContains) {
				t.Fatalf("command_error error = %q, want containing %q; payload=%s", protocol.Deref(event.Error), errorContains, string(outbound.payload))
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for command_error for %s", cmd)
		}
	}
}

type failingSpawnBackend struct {
	err error
}

func (b *failingSpawnBackend) Spawn(context.Context, ptybackend.SpawnOptions) error {
	return b.err
}
func (b *failingSpawnBackend) Attach(context.Context, string, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, errors.New("attach unsupported")
}
func (b *failingSpawnBackend) Input(context.Context, string, []byte) error { return nil }
func (b *failingSpawnBackend) Resize(context.Context, string, uint16, uint16, uint16, uint16) (ptybackend.ResizeResult, error) {
	return ptybackend.ResizeResult{Changed: true}, nil
}
func (b *failingSpawnBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}
func (b *failingSpawnBackend) Kill(context.Context, string, syscall.Signal) error { return nil }
func (b *failingSpawnBackend) Remove(context.Context, string) error               { return nil }
func (b *failingSpawnBackend) SessionIDs(context.Context) []string                { return nil }
func (b *failingSpawnBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *failingSpawnBackend) Shutdown(context.Context) error { return nil }

func TestListWorkspacesLocalWorkspaceHasEmptyEndpointID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-local-endpoint-id"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Local Workspace",
		Directory: cwd,
	})

	workspaces := d.listWorkspaces()
	var found *protocol.Workspace
	for i := range workspaces {
		if workspaces[i].ID == workspaceID {
			found = &workspaces[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("listWorkspaces() did not include %s: %+v", workspaceID, workspaces)
	}
	if found.EndpointID != nil && *found.EndpointID != "" {
		t.Fatalf("local workspace EndpointID = %v, want nil/empty", found.EndpointID)
	}
}
