package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func TestWorkspaceSessionProtocolLifecycleMatchesAppOrder(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-real-app-order"
	sessionID := "session-shell-1"
	paneID := "pane-session-shell-1"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Real App Order",
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
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusSpawning, "")

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("shell"),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusReady, "")
	if session := d.store.Get(sessionID); session == nil {
		t.Fatalf("session %s was not registered", sessionID)
	}

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutClosePane,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, workspaceID, paneID, true)
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("session %s still registered after closing its workspace pane", sessionID)
	}
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil {
		t.Fatalf("workspace layout still exists after closing only pane: %+v", snapshot)
	}
	if workspace := d.store.GetWorkspace(workspaceID); workspace != nil {
		t.Fatalf("workspace still exists after closing its only session pane: %+v", workspace)
	}
	if _, _, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); ok {
		t.Fatalf("session %s still has a workspace pane mapping", sessionID)
	}
}

func TestWorkspaceSessionProtocolBareSpawnEnsuresLayoutPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-bare-spawn"
	sessionID := "session-bare-spawn"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Bare Spawn",
		Directory: cwd,
	})
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("bare shell"),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)

	gotWorkspaceID, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID)
	if !ok {
		t.Fatalf("bare spawn did not create a workspace layout pane for %s", sessionID)
	}
	if gotWorkspaceID != workspaceID {
		t.Fatalf("ensured pane workspace = %q, want %q", gotWorkspaceID, workspaceID)
	}
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusReady, "")

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if len(snapshot.Panes) != 1 {
		t.Fatalf("panes = %+v, want exactly the ensured pane", snapshot.Panes)
	}
	if snapshot.Panes[0].Title != "bare shell" {
		t.Fatalf("ensured pane title = %q, want the session label", snapshot.Panes[0].Title)
	}
}

func TestWorkspaceSessionProtocolBareSpawnFailureCreatesNoPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-bare-spawn-fails"
	sessionID := "session-bare-spawn-fails"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Bare Spawn Fails",
		Directory: cwd,
	})
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("bare shell"),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, false)

	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("failed bare spawn registered session %s", sessionID)
	}
	if _, _, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); ok {
		t.Fatalf("failed bare spawn left a ghost pane for session %s", sessionID)
	}
	if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil && len(snapshot.Panes) != 0 {
		t.Fatalf("workspace layout has panes after failed bare spawn: %+v", snapshot.Panes)
	}
}

func TestWorkspaceSessionProtocolSpawnAdoptsPreCreatedPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-adopt"
	sessionID := "session-adopt"
	paneID := "pane-session-adopt"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Adopt",
		Directory: cwd,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr("custom title"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("different label"),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if len(snapshot.Panes) != 1 {
		t.Fatalf("panes = %+v, want the single pre-created pane adopted", snapshot.Panes)
	}
	if snapshot.Panes[0].PaneID != paneID || snapshot.Panes[0].Title != "custom title" {
		t.Fatalf("adopted pane = %+v, want id %q with its original title", snapshot.Panes[0], paneID)
	}
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusReady, "")
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
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("shell"),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
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

	backend := &fakeSpawnBackend{}
	backend.onKill = func() {
		if session := d.store.Get(sessionID); session == nil {
			t.Fatalf("session %s was removed before pty kill", sessionID)
		}
		if snapshot := d.store.GetWorkspaceLayout(workspaceID); snapshot != nil {
			t.Fatalf("workspace layout still referenced session %s when pty kill began: %+v", sessionID, snapshot)
		}
		if _, _, ok := d.store.FindWorkspaceLayoutPaneBySessionID(sessionID); ok {
			t.Fatalf("workspace pane mapping for session %s still existed when pty kill began", sessionID)
		}
	}
	d.ptyBackend = backend

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
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("shell"),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
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

func TestWorkspaceLayoutStartupReconcileRemovesOrphanButKeepsPendingSpawn(t *testing.T) {
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
	if pending := d.store.GetWorkspaceLayout("workspace-pending"); pending == nil || !workspacelayout.HasPane(pending.Layout, "pane-session-pending") {
		t.Fatalf("valid pending spawn was removed: %+v", pending)
	}
}

func TestWorkspaceSessionProtocolSpawnFailureMarksPaneFailed(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-spawn-fails"
	sessionID := "session-fails"
	paneID := "pane-session-fails"
	cwd := t.TempDir()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Spawn Fails",
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
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("shell"),
		Cwd:         cwd,
		Agent:       protocol.AgentShellValue,
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, false)
	expectPaneStatus(t, d, workspaceID, paneID, workspacelayout.PaneStatusFailed, "boom")
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("failed spawn registered session %s", sessionID)
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
		Directory: originalDirectory, WorkspaceID: workspaceID, State: protocol.SessionStateIdle,
		StateSince: "before", StateUpdatedAt: "before", LastSeen: "before",
	}
	if err := d.store.AddChecked(original); err != nil {
		t.Fatal(err)
	}

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: original.ID, Label: protocol.Ptr("replacement"),
		Cwd: requestedDirectory, Agent: protocol.AgentShellValue, WorkspaceID: workspaceID, Cols: 80, Rows: 24,
	})
	expectSpawnResult(t, client, original.ID, false)
	got := d.store.Get(original.ID)
	if got == nil || got.Directory != originalDirectory || got.Label != original.Label || got.LastSeen != original.LastSeen {
		t.Fatalf("session after failed respawn = %+v, want restored %+v", got, original)
	}
}

func TestWorkspaceSessionProtocolRejectsShellSpawnWithoutWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sessionID := "session-shell-without-workspace"

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:   protocol.CmdSpawnSession,
		ID:    sessionID,
		Label: protocol.Ptr("shell"),
		Cwd:   t.TempDir(),
		Agent: protocol.AgentShellValue,
		Cols:  80,
		Rows:  24,
	})

	expectCommandError(t, client, protocol.CmdSpawnSession, "missing workspace_id")
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("shell spawn without workspace registered session %s", sessionID)
	}
}

func TestWorkspaceSessionProtocolRejectsShellSpawnForUnknownWorkspace(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	client := newWorkspaceProtocolTestClient()
	sessionID := "session-shell-unknown-workspace"

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Label:       protocol.Ptr("shell"),
		Cwd:         t.TempDir(),
		Agent:       protocol.AgentShellValue,
		Cols:        80,
		Rows:        24,
		WorkspaceID: "missing-workspace",
	})

	expectCommandError(t, client, protocol.CmdSpawnSession, "unknown workspace")
	if session := d.store.Get(sessionID); session != nil {
		t.Fatalf("shell spawn for unknown workspace registered session %s", sessionID)
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
		Direction:    protocol.Ptr(protocol.WorkspaceLayoutSplitDirectionVertical),
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
		Direction:    protocol.Ptr(protocol.WorkspaceLayoutSplitDirectionVertical),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-2", true)

	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-1",
		Edge:         protocol.WorkspaceLayoutDockEdgeRight,
		TileID:       "pane-2",
		TileKind:     "markdown",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "pane-2", false)
	afterCollision := d.store.GetWorkspaceLayout(workspaceID)
	if !workspacelayout.HasPane(afterCollision.Layout, "pane-2") || workspacelayout.HasTile(afterCollision.Layout, "pane-2") {
		t.Fatalf("pane id collision mutated layout: %+v", afterCollision.Layout)
	}

	if err := d.dockTile(workspaceID, "pane-1", "tile-md", "markdown", "/tmp/notes.md", "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
		t.Fatalf("dockTile: %v", err)
	}

	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("workspace layout missing after docking tile")
	}
	if !workspacelayout.HasTile(snapshot.Layout, "tile-md") {
		t.Fatalf("tile not present after dock: %+v", snapshot.Layout)
	}
	if ids := workspacelayout.PaneIDs(snapshot.Layout); len(ids) != 2 {
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
	if len(afterPaneCollision.Panes) != 2 || !workspacelayout.HasTile(afterPaneCollision.Layout, "tile-md") {
		t.Fatalf("tile id collision mutated layout: %+v", afterPaneCollision)
	}

	encoded, err := workspacelayout.EncodeLayout(snapshot.Layout)
	if err != nil {
		t.Fatalf("EncodeLayout: %v", err)
	}
	decoded, err := workspacelayout.DecodeLayout(encoded)
	if err != nil {
		t.Fatalf("DecodeLayout: %v", err)
	}
	if !workspacelayout.HasTile(decoded, "tile-md") {
		t.Fatal("tile lost across layout JSON reload")
	}

	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-2",
		Edge:         protocol.WorkspaceLayoutDockEdgeBottom,
		TileID:       "tile-md",
		TileKind:     "markdown",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "tile-md", true)
	moved := d.store.GetWorkspaceLayout(workspaceID)
	if ids := workspacelayout.TileIDs(moved.Layout); len(ids) != 1 {
		t.Fatalf("tile ids after move = %v, want exactly one", ids)
	}
	if params, ok := workspacelayout.TileParamsByID(moved.Layout, "tile-md"); !ok || params != "/tmp/notes.md" {
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
	if params, ok := workspacelayout.TileParamsByID(unchanged.Layout, "tile-md"); !ok || params != "/tmp/notes.md" {
		t.Fatalf("markdown tile params = (%q, %v), want (%q, true)", params, ok, "/tmp/notes.md")
	}

	d.ensureGardenCollections()
	seedSchema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	for _, seed := range []garden.Seed{
		{ID: "s-old001", Title: "Original seed", Status: garden.StatusPlanted},
		{ID: "s-new002", Title: "Child seed", Status: garden.StatusPlanted},
	} {
		if _, err := d.plantSeed(*seedSchema, seed); err != nil {
			t.Fatalf("plant seed %s: %v", seed.ID, err)
		}
	}
	if err := d.dockTile(workspaceID, "pane-1", "tile-seed", string(workspacelayout.TileKindSeed), "s-old001", "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
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
	if params, ok := workspacelayout.TileParamsByID(seedUpdated.Layout, "tile-seed"); !ok || params != "s-new002" {
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
	if params, ok := workspacelayout.TileParamsByID(d.store.GetWorkspaceLayout(workspaceID).Layout, "tile-seed"); !ok || params != "s-new002" {
		t.Fatalf("missing seed retarget changed params = (%q, %v), want (%q, true)", params, ok, "s-new002")
	}

	d.store.Add(&protocol.Session{ID: "session-2", Label: "Two", WorkspaceID: workspaceID, Directory: cwd})
	d.handleWorkspaceLayoutUpdateTile(client, &protocol.WorkspaceLayoutUpdateTileMessage{
		Cmd:           protocol.CmdWorkspaceLayoutUpdateTile,
		WorkspaceID:   workspaceID,
		TileID:        "tile-md",
		TileParams:    "/tmp/notes.md",
		TileSessionID: protocol.Ptr("session-unknown"),
		RequestID:     "request-retarget-unknown-session",
	})
	expectWorkspaceLayoutActionResultIDsAndRequestID(t, client, protocol.CmdWorkspaceLayoutUpdateTile, workspaceID, "", "", "tile-md", "request-retarget-unknown-session", false)
	if sessionID, ok := workspacelayout.TileSessionIDByID(d.store.GetWorkspaceLayout(workspaceID).Layout, "tile-md"); ok && sessionID == "session-unknown" {
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
	if sessionID, ok := workspacelayout.TileSessionIDByID(retargeted.Layout, "tile-md"); !ok || sessionID != "session-2" {
		t.Fatalf("markdown tile session = (%q, %v), want (%q, true)", sessionID, ok, "session-2")
	}
	if params, ok := workspacelayout.TileParamsByID(retargeted.Layout, "tile-md"); !ok || params != "/tmp/notes.md" {
		t.Fatalf("markdown tile params after retarget = (%q, %v), want unchanged %q", params, ok, "/tmp/notes.md")
	}

	if err := d.dockTile(workspaceID, "pane-2", "tile-browser", "browser", "https://example.com/", "", protocol.WorkspaceLayoutDockEdgeRight, nil); err != nil {
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
	if params, ok := workspacelayout.TileParamsByID(updated.Layout, "tile-browser"); !ok || params != "https://example.com/docs" {
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
	if sessionID, ok := workspacelayout.TileSessionIDByID(combined.Layout, "tile-browser"); !ok || sessionID != "session-2" {
		t.Fatalf("browser tile session after combined update = (%q, %v), want (%q, true) — params save clobbered the rebind", sessionID, ok, "session-2")
	}
	if params, ok := workspacelayout.TileParamsByID(combined.Layout, "tile-browser"); !ok || params != "https://example.com/combined" {
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
	if params, ok := workspacelayout.TileParamsByID(afterRejectedURL.Layout, "tile-browser"); !ok || params != "https://example.com/combined" {
		t.Fatalf("browser tile params after rejected URL = (%q, %v), want (%q, true)", params, ok, "https://example.com/combined")
	}

	if err := d.dockTile(workspaceID, "pane-1", "tile-notebook", string(workspacelayout.TileKindNotebook), "", "", protocol.WorkspaceLayoutDockEdgeLeft, nil); err != nil {
		t.Fatalf("dock notebook tile: %v", err)
	}
	if params, ok := workspacelayout.TileParamsByID(d.store.GetWorkspaceLayout(workspaceID).Layout, "tile-notebook"); !ok || params != "" {
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
	if params, ok := workspacelayout.TileParamsByID(d.store.GetWorkspaceLayout(workspaceID).Layout, "tile-notebook"); !ok || params != "/notes/knowledge/decisions.md" {
		t.Fatalf("notebook tile params after update = (%q, %v), want the opened path", params, ok)
	}

	d.handleWorkspaceLayoutUndockTile(client, &protocol.WorkspaceLayoutUndockTileMessage{
		Cmd:         protocol.CmdWorkspaceLayoutUndockTile,
		WorkspaceID: workspaceID,
		TileID:      "tile-md",
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutUndockTile, workspaceID, "", "", "tile-md", true)
	after := d.store.GetWorkspaceLayout(workspaceID)
	if workspacelayout.HasTile(after.Layout, "tile-md") {
		t.Fatal("tile still present after undock")
	}
	if ids := workspacelayout.PaneIDs(after.Layout); len(ids) != 2 {
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
		Edge:         protocol.WorkspaceLayoutDockEdgeRight,
		TileID:       "tile-notebook",
		TileKind:     string(workspacelayout.TileKindNotebook),
		TileParams:   protocol.Ptr("/notes/knowledge/decisions.md"),
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "tile-notebook", true)
	fresh := d.store.GetWorkspaceLayout(workspaceID)
	if params, ok := workspacelayout.TileParamsByID(fresh.Layout, "tile-notebook"); !ok || params != "/notes/knowledge/decisions.md" {
		t.Fatalf("fresh tile params = (%q, %v), want (%q, true)", params, ok, "/notes/knowledge/decisions.md")
	}

	d.handleWorkspaceLayoutDockTile(client, &protocol.WorkspaceLayoutDockTileMessage{
		Cmd:          protocol.CmdWorkspaceLayoutDockTile,
		WorkspaceID:  workspaceID,
		AnchorPaneID: "pane-1",
		Edge:         protocol.WorkspaceLayoutDockEdgeBottom,
		TileID:       "tile-notebook",
		TileKind:     string(workspacelayout.TileKindNotebook),
	})
	expectWorkspaceLayoutActionResultIDs(t, client, protocol.CmdWorkspaceLayoutDockTile, workspaceID, "", "", "tile-notebook", true)
	moved := d.store.GetWorkspaceLayout(workspaceID)
	if params, ok := workspacelayout.TileParamsByID(moved.Layout, "tile-notebook"); !ok || params != "/notes/knowledge/decisions.md" {
		t.Fatalf("moved tile params = (%q, %v), want unchanged (%q, true)", params, ok, "/notes/knowledge/decisions.md")
	}
}

func firstSplitID(node workspacelayout.Node) string {
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

func findSplit(node workspacelayout.Node, splitID string) *workspacelayout.Node {
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

func expectSpawnResult(t *testing.T, client *wsClient, sessionID string, success bool) {
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
			return
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

func expectPaneStatus(t *testing.T, d *Daemon, workspaceID, paneID string, status workspacelayout.PaneStatus, errorContains string) {
	t.Helper()
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatalf("workspace layout %s not found", workspaceID)
	}
	for _, pane := range snapshot.Panes {
		if pane.PaneID != paneID {
			continue
		}
		if pane.Status != status {
			t.Fatalf("pane %s status = %q, want %q", paneID, pane.Status, status)
		}
		if errorContains != "" && !strings.Contains(pane.Error, errorContains) {
			t.Fatalf("pane %s error = %q, want containing %q", paneID, pane.Error, errorContains)
		}
		return
	}
	t.Fatalf("pane %s not found in workspace %s", paneID, workspaceID)
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
func (b *failingSpawnBackend) Resize(context.Context, string, uint16, uint16, uint16, uint16) (bool, error) {
	return true, nil
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
