package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type gitExecutorFunc func(context.Context, gitTask, func(context.Context, *attngit.Client) error) error

func (f gitExecutorFunc) Run(ctx context.Context, task gitTask, run func(context.Context, *attngit.Client) error) error {
	return f(ctx, task, run)
}

func (gitExecutorFunc) Close(error) {}

func TestWorktreeMaintenanceForegroundPreemptsObservation(t *testing.T) {
	var coordinator worktreeMaintenanceCoordinator
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- coordinator.RunSweep(context.Background(), func(lease *worktreeSweepLease) error {
			close(started)
			<-lease.Context().Done()
			return context.Cause(lease.Context())
		})
	}()
	<-started

	if err := coordinator.RunForeground(context.Background(), "test", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, errWorktreeSweepPreempted) {
		t.Fatalf("sweep error = %v, want preemption", err)
	}
}

func TestWorktreeMaintenanceForegroundPreemptsBlockedOriginLookup(t *testing.T) {
	fakeBin := t.TempDir()
	startedFIFO := filepath.Join(t.TempDir(), "git-started")
	if err := syscall.Mkfifo(startedFIFO, 0o600); err != nil {
		t.Fatal(err)
	}
	releaseFIFO := filepath.Join(t.TempDir(), "git-release")
	if err := syscall.Mkfifo(releaseFIFO, 0o600); err != nil {
		t.Fatal(err)
	}
	fakeGit := filepath.Join(fakeBin, "git")
	script := "#!/bin/sh\nif [ \"$1\" = remote ] && [ \"$2\" = get-url ] && [ \"$3\" = origin ]; then\n  printf x > \"$ATTN_GIT_STARTED_FIFO\"\n  read ignored < \"$ATTN_GIT_RELEASE_FIFO\"\nfi\nexit 1\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ATTN_GIT_STARTED_FIFO", startedFIFO)
	t.Setenv("ATTN_GIT_RELEASE_FIFO", releaseFIFO)

	started := make(chan error, 1)
	go func() {
		fifo, err := os.Open(startedFIFO)
		if err == nil {
			defer fifo.Close()
			_, err = io.ReadFull(fifo, make([]byte, 1))
		}
		started <- err
	}()

	d := sweepDaemon(t)
	repo := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	finished := make(chan error, 1)
	go func() {
		finished <- d.worktreeMaintenance.RunSweep(ctx, func(lease *worktreeSweepLease) error {
			return d.refreshMergedPullRequestsContext(lease.Context(), repo, time.Now())
		})
	}()
	if err := <-started; err != nil {
		t.Fatal(err)
	}

	if err := d.worktreeMaintenance.RunForeground(context.Background(), "test foreground", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, errWorktreeSweepPreempted) {
		t.Fatalf("blocked origin lookup error = %v, want preemption cause", err)
	}
}

func TestSessionRegistrationAcquiresWorktreeMaintenanceBeforeGitIdentity(t *testing.T) {
	d := sweepDaemon(t)
	leaseHeld := false
	d.gitExec = gitExecutorFunc(func(ctx context.Context, _ gitTask, _ func(context.Context, *attngit.Client) error) error {
		err := d.worktreeMaintenance.RunSweep(ctx, func(lease *worktreeSweepLease) error {
			return lease.TryDelete(func(context.Context) error { return nil }, func(context.Context) error { return nil })
		})
		leaseHeld = errors.Is(err, errWorktreeSweepPreempted)
		if !leaseHeld {
			return fmt.Errorf("session identity ran without the foreground maintenance lease")
		}
		return nil
	})
	conn := &syncConn{}
	d.handleRegister(conn, &protocol.RegisterMessage{
		ID: "bare-cli", Label: protocol.Ptr("bare-cli"), Dir: t.TempDir(),
		Agent: protocol.Ptr(protocol.SessionAgentCodex), WorkspaceID: "workspace-bare-cli",
	})
	if !leaseHeld {
		t.Fatal("session registration inspected Git before acquiring the foreground maintenance lease")
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

func TestWorktreeSweepInventoriesEachRepositoryOnceWhenEveryRowIsCheap(t *testing.T) {
	d := sweepDaemon(t)
	now := time.Now()
	repoA, repoB := "/repo/a", "/repo/b"
	rows := []*store.Worktree{
		{Path: "/worktree/a1", MainRepo: repoA, CreatedAt: now},
		{Path: "/worktree/a2", MainRepo: repoA, CreatedAt: now},
		{Path: "/worktree/b1", MainRepo: repoB, CreatedAt: now},
	}
	for _, row := range rows {
		d.store.AddWorktree(row)
	}
	calls := map[string]int{}
	d.worktreeListStates = func(_ context.Context, repo string) ([]attngit.WorktreeState, error) {
		calls[repo]++
		states := []attngit.WorktreeState{{Path: repo, HeadSHA: "main"}}
		for _, row := range rows {
			if row.MainRepo == repo {
				states = append(states, attngit.WorktreeState{Path: row.Path, Branch: "feature", HeadSHA: "head"})
			}
		}
		return states, nil
	}

	refreshed, removed, kept, err := d.runWorktreeSweep(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed != 2 || removed != 0 || kept != 3 {
		t.Fatalf("stats = %d refreshed, %d removed, %d kept", refreshed, removed, kept)
	}
	if calls[repoA] != 1 || calls[repoB] != 1 {
		t.Fatalf("inventory calls = %+v, want one per repository", calls)
	}
}

func TestWorktreeSweepPreemptionPersistsNoPartialObservationAndStopsCandidates(t *testing.T) {
	d := sweepDaemon(t)
	now := time.Now()
	repo := "/repo/main"
	paths := []string{"/worktree/one", "/worktree/two"}
	for _, path := range paths {
		d.store.AddWorktree(&store.Worktree{Path: path, MainRepo: repo, CreatedAt: now.Add(-30 * 24 * time.Hour)})
	}
	d.worktreeListStates = func(context.Context, string) ([]attngit.WorktreeState, error) {
		return []attngit.WorktreeState{
			{Path: repo, HeadSHA: "main"},
			{Path: paths[0], Branch: "one", HeadSHA: "one-head"},
			{Path: paths[1], Branch: "two", HeadSHA: "two-head"},
		}, nil
	}
	d.worktreeRepositoryFacts = func(context.Context, string, time.Time) (*repositoryFacts, error) {
		return &repositoryFacts{repo: repo, integrationBranch: "main", integrationSHA: "main"}, nil
	}
	started := make(chan struct{})
	observed := 0
	d.worktreeObserveCandidate = func(ctx context.Context, _ *repositoryFacts, _ attngit.WorktreeState, _ time.Time) (store.WorktreeObservation, error) {
		observed++
		close(started)
		<-ctx.Done()
		return store.WorktreeObservation{Dirty: true, DirtyFiles: 99}, context.Cause(ctx)
	}
	done := make(chan error, 1)
	go func() {
		_, err := d.worktreeSweepHandler(context.Background(), nil)
		done <- err
	}()
	<-started
	if err := d.worktreeMaintenance.RunForeground(context.Background(), "test foreground", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("preempted handler error = %v", err)
	}
	if observed != 1 {
		t.Fatalf("observed %d candidates, want only the interrupted first candidate", observed)
	}
	for _, path := range paths {
		row := d.store.GetWorktree(path)
		if row.ObservedAt != "" || row.RefreshError != "" || row.DirtyFiles != 0 {
			t.Fatalf("preemption persisted candidate facts for %s: %+v", path, row)
		}
	}
}

func TestCheapWorktreeSweepVerdictKeepsYoungErrorsAndLockedWorktrees(t *testing.T) {
	now := time.Now()
	row := &store.Worktree{
		Path: "/repo/worktree", CreatedAt: now.Add(-time.Hour),
		RefreshError: "old failure",
	}
	verdict, cheap := cheapWorktreeSweepVerdict(row, attngit.WorktreeState{Path: row.Path}, sweepContext{}, now, 14*24*time.Hour)
	if !cheap || verdict.Status != store.WorktreeSweepScheduled {
		t.Fatalf("young row = cheap %v, status %q", cheap, verdict.Status)
	}
	verdict, cheap = cheapWorktreeSweepVerdict(row, attngit.WorktreeState{Path: row.Path, Locked: true}, sweepContext{}, now, 0)
	if !cheap || verdict.Status != store.WorktreeSweepUnknown {
		t.Fatalf("locked row = cheap %v, status %q", cheap, verdict.Status)
	}
}

func TestWorktreeMaintenanceSharedGateMakesDeleteYield(t *testing.T) {
	var coordinator worktreeMaintenanceCoordinator
	foregroundEntered := make(chan struct{})
	releaseForeground := make(chan struct{})
	foregroundDone := make(chan struct{})
	go func() {
		_ = coordinator.RunForeground(context.Background(), "test", func(context.Context) error {
			close(foregroundEntered)
			<-releaseForeground
			return nil
		})
		close(foregroundDone)
	}()
	<-foregroundEntered

	err := coordinator.RunSweep(context.Background(), func(lease *worktreeSweepLease) error {
		return lease.TryDelete(func(context.Context) error { return nil }, func(context.Context) error { return nil })
	})
	if !errors.Is(err, errWorktreeSweepPreempted) {
		t.Fatalf("delete error = %v, want preemption", err)
	}
	close(releaseForeground)
	<-foregroundDone
}

func TestWorktreeMaintenanceDeleteCommitBlocksForeground(t *testing.T) {
	var coordinator worktreeMaintenanceCoordinator
	deleteEntered := make(chan struct{})
	releaseDelete := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- coordinator.RunSweep(context.Background(), func(lease *worktreeSweepLease) error {
			return lease.TryDelete(func(context.Context) error { return nil }, func(context.Context) error {
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
		foregroundDone <- coordinator.RunForeground(context.Background(), "test", func(context.Context) error {
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
