package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

var errSpawnRefusedInThisTest = errors.New("the pty backend refuses to spawn in this test")

func reopenDaemonWithBackend(t *testing.T, d *Daemon) *fakeSpawnBackend {
	t.Helper()
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	t.Cleanup(d.stopEventBus)
	return backend
}

func TestAFailedReopenPutsTheCloseBackAsItWas(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	backend.spawnErr = errSpawnRefusedInThisTest
	writeCodexRolloutFixture(t, "conv-failed")
	closeReopenSession(t, d, reopenSession{
		ID: "failed", Directory: t.TempDir(), Agent: "codex", Resume: "conv-failed",
		ClosedBy: "sess-boss", Reason: "brief delivered",
	})

	closed := d.store.SessionLedgerEntry("failed")
	if closed == nil {
		t.Fatal("the fixture did not close the session")
	}
	closedAt := protocol.Deref(closed.ClosedAt)

	if _, err := d.reopenSession("failed", "", ""); err == nil {
		t.Fatal("the reopen reported success although the spawn failed")
	}

	entry := d.store.SessionLedgerEntry("failed")
	if entry == nil {
		t.Fatal("the failed reopen took the session out of the ledger")
	}
	if at := protocol.Deref(entry.ClosedAt); at != closedAt {
		t.Errorf("closed_at = %q, want the original close time %q", at, closedAt)
	}
	if by := protocol.Deref(entry.ClosedBy); by != "sess-boss" {
		t.Errorf("closed_by = %q, want the original closer restored", by)
	}
	if reason := protocol.Deref(entry.CloseReason); reason != "brief delivered" {
		t.Errorf("close_reason = %q, want the original reason restored", reason)
	}
}

func TestAFailedReopenAfterWorktreeCreationRollsBackOutsideGitAdmission(t *testing.T) {
	d, _, worktree := closedWorktreeWithDeletedDirectory(t, "failed-worktree", "feat/failed-worktree", false)
	backend := reopenDaemonWithBackend(t, d)
	backend.spawnErr = errSpawnRefusedInThisTest
	closedAt := protocol.Deref(d.store.SessionLedgerEntry("failed-worktree").ClosedAt)

	_, err := d.reopenSession(
		"failed-worktree",
		protocol.SessionReopenActionRecreateWorktreeAndReopen,
		"",
	)
	if err == nil {
		t.Fatal("reopen succeeded although the spawn failed")
	}
	if errors.Is(err, ErrNestedGitExecution) {
		t.Fatalf("rollback re-entered Git admission from an admitted callback: %v", err)
	}
	if _, statErr := os.Stat(worktree); !os.IsNotExist(statErr) {
		t.Fatalf("rollback left recreated worktree %s behind: %v", worktree, statErr)
	}
	entry := d.store.SessionLedgerEntry("failed-worktree")
	if entry == nil || protocol.Deref(entry.ClosedAt) != closedAt {
		t.Fatalf("rollback close = %+v, want generation %s restored", entry, closedAt)
	}
}

func TestAFailedReopenKeepsThePaneThatOutlivedTheClose(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	backend.spawnErr = errSpawnRefusedInThisTest
	writeCodexRolloutFixture(t, "conv-pane")
	directory := t.TempDir()

	client := newWorkspaceProtocolTestClient()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-survivor", Title: "Survivor", Directory: directory,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: "workspace-survivor",
		PaneID: protocol.Ptr("pane-survivor"), SessionID: "survivor", Title: protocol.Ptr("Survivor"),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane,
		"workspace-survivor", "pane-survivor", true)

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "survivor", Label: "survivor", Agent: protocol.SessionAgentCodex,
		Directory: directory, WorkspaceID: "workspace-survivor", State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.persistResumeSessionID("survivor", "conv-pane")
	d.closeSession("survivor", store.SessionClose{By: store.SessionClosedByUser})

	verdict := decidedReopenVerdict(t, d, "survivor")
	if verdict.PanePlan != reopenPlaceReuse {
		t.Fatalf("pane plan = %s, want the surviving pane reused", verdict.PanePlan)
	}

	if _, err := d.reopenSession("survivor", "", ""); err == nil {
		t.Fatal("the reopen reported success although the spawn failed")
	}

	holder, paneID, ok := d.store.FindWorkspaceLayoutPaneBySessionID("survivor")
	if !ok || paneID != "pane-survivor" || holder != "workspace-survivor" {
		t.Errorf("pane after the failed reopen = %q in %q (found %v), want pane-survivor kept in workspace-survivor",
			paneID, holder, ok)
	}
}
