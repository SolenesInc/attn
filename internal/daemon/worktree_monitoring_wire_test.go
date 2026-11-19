package daemon_test

import (
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWorktreeMonitoringSurvivesItsLastCheckoutAndADaemonRestart(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	created := createWorktree(t, app, repo, "feature")
	if err := cli.DeleteWorktree(created, false); err != nil {
		t.Fatal(err)
	}
	w.restart()
	app, cli = w.App(), w.Client()
	external := filepath.Join(filepath.Dir(repo), "external")
	runGit(t, repo, "worktree", "add", "-b", "external", external)
	refreshWorktrees(t, cli)
	observed := sweepAwaitState(app, external, func(protocol.Worktree) bool { return true })
	if observed.MainRepo != repo || observed.Branch != "external" || protocol.Deref(observed.Origin) != "git" {
		t.Errorf("later external checkout = %+v, want external of %s discovered from Git", observed, repo)
	}
}

func TestACreationAttemptEnrollsTheRepositoryEvenWhenGitRefusesIt(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	result := testworld.Request(app, protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "main"},
		protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
	if result.Success {
		t.Fatal("creating a new branch named main succeeded even though it already exists")
	}
	w.restart()
	app, cli := w.App(), w.Client()
	external := filepath.Join(filepath.Dir(repo), "external")
	runGit(t, repo, "worktree", "add", "-b", "external", external)
	refreshWorktrees(t, cli)
	if observed := sweepAwaitState(app, external, func(protocol.Worktree) bool { return true }); observed.MainRepo != repo {
		t.Errorf("external checkout after the refused creation and restart = %+v, want discovery in %s", observed, repo)
	}
}

func TestBrowsingARepositoryReadsFreshGitWithoutEnrollingIt(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	unknown := newRepo(t, "unknown")
	external := filepath.Join(filepath.Dir(unknown), "external")
	runGit(t, unknown, "worktree", "add", "-b", "external", external)
	info := testworld.Request(app, protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: unknown},
		protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
	if !info.Success || info.Info == nil || info.Info.CurrentBranch != "main" {
		t.Fatalf("repository query = %+v", info)
	}
	if rows := info.Info.Worktrees; len(rows) != 1 || rows[0].Path != external || rows[0].Branch != "external" || rows[0].CreatedAt != nil {
		t.Errorf("query = %+v, want fresh external checkout without an invented creation date", rows)
	}
	if tracked, err := cli.WorktreeList(unknown, 0); err != nil || len(tracked.Worktrees) != 0 {
		t.Fatalf("browsing registered worktrees: %+v, %v", tracked, err)
	}

	known := newRepo(t, "known")
	witness := createWorktree(t, app, known, "witness")
	refreshWorktrees(t, cli)
	sweepAwaitState(app, witness, func(protocol.Worktree) bool { return true })
	w.restart()
	if tracked, err := w.Client().WorktreeList(unknown, 0); err != nil || len(tracked.Worktrees) != 0 {
		t.Errorf("after a completed sweep and restart browsing enrolled unknown: %+v, %v", tracked, err)
	}
}

func TestRepositoryQueriesLeaveRegistryRemovalToTheSweep(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		repo := newRepo(t, "shop")
		path := createWorktree(t, app, repo, "feature")
		createdAt := sweepCreatedAt(t, cli, repo, path)
		runGit(t, path, "branch", "-m", "renamed")
		info := testworld.Request(app, protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: repo},
			protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
		if !info.Success || info.Info == nil || len(info.Info.Worktrees) != 1 {
			t.Fatalf("repository query = %+v", info)
		}
		wt := info.Info.Worktrees[0]
		if wt.Branch != "renamed" || protocol.Deref(wt.CreatedAt) != createdAt.Format(time.RFC3339) {
			t.Errorf("fresh Git and stored metadata = %+v", wt)
		}
		runGit(t, repo, "worktree", "remove", path)
		info = testworld.Request(app, protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: repo},
			protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
		if !info.Success || info.Info == nil {
			t.Fatalf("repository query = %+v", info)
		}
		if len(info.Info.Worktrees) != 0 {
			t.Errorf("the repository query still shows a removed checkout: %+v", info.Info.Worktrees)
		}
		if tracked, err := cli.WorktreeList(repo, 0); err != nil || !slices.Equal(worktreeCreatePaths(tracked.Worktrees), []string{path}) {
			t.Fatalf("query changed registry: %+v, %v", tracked, err)
		}
		refreshWorktrees(t, cli)
		synctest.Wait()
		if tracked, err := cli.WorktreeList(repo, 0); err != nil || len(tracked.Worktrees) != 0 {
			t.Errorf("sweep left the removed checkout registered: %+v, %v", tracked, err)
		}
	})
}

func TestRepositoryQueriesLetAutomaticCleanupFinish(t *testing.T) {
	t.Setenv("ATTN_WORKTREE_SWEEP_IDLE_DAYS", "0")
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	path := createWorktree(t, app, repo, "feature")
	inspection := newMaintenanceGitGate(t, "status --porcelain --untracked-files=all")
	inspection.arm(t)
	refreshWorktrees(t, cli)
	inspection.awaitBlocked(t)
	testworld.Request(app, protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: repo},
		protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
	inspection.release(t)
	if swept := sweepAwaitSwept(app, path); swept.Action != "removed" {
		t.Errorf("cleanup after queries = %+v, want the pass to finish removing the eligible checkout", swept)
	}
}
