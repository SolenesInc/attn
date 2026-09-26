package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type gitExecutorFunc func(context.Context, gitTask, func(context.Context, *attngit.Client) error) error

func worktreeAutomaticCleanupExcluded(d *Daemon) bool {
	if d.worktreeMaintenance.gate.TryLock() {
		d.worktreeMaintenance.gate.Unlock()
		return false
	}
	return true
}

func (f gitExecutorFunc) Run(ctx context.Context, task gitTask, run func(context.Context, *attngit.Client) error) error {
	return f(ctx, task, run)
}

func (gitExecutorFunc) Close(error) {}

func TestSessionRegistrationAcquiresWorktreeMaintenanceBeforeGitIdentity(t *testing.T) {
	d := sweepDaemon(t)
	leaseHeld := false
	d.gitExec = gitExecutorFunc(func(ctx context.Context, _ gitTask, _ func(context.Context, *attngit.Client) error) error {
		leaseHeld = worktreeAutomaticCleanupExcluded(d)
		if !leaseHeld {
			return fmt.Errorf("session identity ran without the foreground automatic cleanup exclusion")
		}
		return nil
	})
	conn := &syncConn{}
	d.handleRegister(conn, &protocol.RegisterMessage{
		ID: "bare-cli", Label: protocol.Ptr("bare-cli"), Dir: t.TempDir(),
		Agent: protocol.Ptr(protocol.SessionAgentCodex), WorkspaceID: "workspace-bare-cli",
	})
	if !leaseHeld {
		t.Fatal("session registration inspected Git before acquiring the foreground automatic cleanup exclusion")
	}
}

func TestTrackedRepositoriesUsesStoredMainRepoAndBackfillsLegacySessions(t *testing.T) {
	d := sweepDaemon(t)
	d.store.Add(&protocol.Session{ID: "known", Directory: "/missing/session", MainRepo: protocol.Ptr("/missing/main")})
	repos, err := d.trackedRepositoriesContext(context.Background())
	if err != nil || len(repos) != 1 || repos[0] != "/missing/main" {
		t.Fatalf("stored main repo discovery = %v, %v", repos, err)
	}

	root := t.TempDir()
	mainRepo := filepath.Join(root, "main")
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(filepath.Join(worktree, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.store.AddWorktree(&store.Worktree{Path: worktree, MainRepo: mainRepo, CreatedAt: time.Now()})
	d.store.Add(&protocol.Session{ID: "legacy", Directory: filepath.Join(worktree, "nested")})
	if _, err := d.trackedRepositoriesContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := protocol.Deref(d.store.Get("legacy").MainRepo); got != mainRepo {
		t.Fatalf("legacy main repo = %q, want %q", got, mainRepo)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := d.trackedRepositoriesContext(context.Background()); err != nil {
		t.Fatalf("backfilled session rediscovered filesystem: %v", err)
	}
}

func TestWorktreeMaintenanceSharedGateMakesDeleteYield(t *testing.T) {
	var coordinator worktreeMaintenanceCoordinator
	foregroundEntered := make(chan struct{})
	releaseForeground := make(chan struct{})
	foregroundDone := make(chan struct{})
	go func() {
		_ = coordinator.ProtectFromAutomaticCleanup(context.Background(), func(foregroundCleanupProtection) error {
			close(foregroundEntered)
			<-releaseForeground
			return nil
		})
		close(foregroundDone)
	}()
	<-foregroundEntered

	err := coordinator.RunSweep(context.Background(), func(lease *worktreeSweepLease) error {
		return lease.TryAutomaticRemoval(func(automaticWorktreeCleanupProtection) error { return nil })
	})
	if !errors.Is(err, errAutomaticWorktreeCleanupPreempted) {
		t.Fatalf("delete error = %v, want preemption", err)
	}
	close(releaseForeground)
	<-foregroundDone
}

func TestWorktreeMaintenanceForegroundOperationsRemainConcurrent(t *testing.T) {
	var coordinator worktreeMaintenanceCoordinator
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- coordinator.ProtectFromAutomaticCleanup(context.Background(), func(foregroundCleanupProtection) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	secondEntered := make(chan struct{})
	if err := coordinator.ProtectFromAutomaticCleanup(context.Background(), func(foregroundCleanupProtection) error {
		close(secondEntered)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	default:
		t.Fatal("second foreground operation did not run concurrently")
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeMaintenanceDeleteCommitBlocksForeground(t *testing.T) {
	var coordinator worktreeMaintenanceCoordinator
	deleteEntered := make(chan struct{})
	releaseDelete := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- coordinator.RunSweep(context.Background(), func(lease *worktreeSweepLease) error {
			return lease.TryAutomaticRemoval(func(automaticWorktreeCleanupProtection) error {
				close(deleteEntered)
				<-releaseDelete
				return nil
			})
		})
	}()
	<-deleteEntered

	foregroundEntered := make(chan struct{})
	foregroundDone := make(chan error, 1)
	go func() {
		foregroundDone <- coordinator.ProtectFromAutomaticCleanup(context.Background(), func(foregroundCleanupProtection) error {
			close(foregroundEntered)
			return nil
		})
	}()
	select {
	case <-foregroundEntered:
		t.Fatal("foreground entered while deletion held the exclusive gate")
	default:
	}
	close(releaseDelete)
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	if err := <-foregroundDone; err != nil {
		t.Fatal(err)
	}
}
