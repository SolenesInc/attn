package daemon_test

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestWorktreeMonitoringSurvivesItsLastCheckoutAndADaemonRestart(t *testing.T) {
	for _, entry := range []string{"new branch", "existing branch"} {
		t.Run(entry, func(t *testing.T) {
			w := newWorld(t)
			app, cli := w.App(), w.Client()
			repo := newRepo(t, "shop")
			var created string
			if entry == "new branch" {
				created = createWorktree(t, app, repo, "feature")
			} else {
				runGit(t, repo, "branch", "feature")
				result := testworld.Request(app, protocol.CreateWorktreeFromBranchMessage{
					Cmd: protocol.CmdCreateWorktreeFromBranch, MainRepo: repo, Branch: "feature",
				}, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
				if !result.Success {
					t.Fatal(protocol.Deref(result.Error))
				}
				created = protocol.Deref(result.Path)
			}
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
		})
	}
}

func TestBrowsingARepositoryReadsFreshGitWithoutEnrollingIt(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	unknown := newRepo(t, "unknown")
	external := filepath.Join(filepath.Dir(unknown), "external")
	runGit(t, unknown, "worktree", "add", "-b", "external", external)
	for _, query := range []any{
		protocol.ListWorktreesMessage{Cmd: protocol.CmdListWorktrees, MainRepo: unknown},
		protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: unknown},
	} {
		var rows []protocol.Worktree
		switch query.(type) {
		case protocol.ListWorktreesMessage:
			rows = testworld.Request(app, query, protocol.EventWorktreesUpdated, func(protocol.WebSocketEvent) bool { return true }).Worktrees
		default:
			info := testworld.Request(app, query, protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
			if !info.Success || info.Info == nil || info.Info.CurrentBranch != "main" {
				t.Fatalf("repository query = %+v", info)
			}
			rows = info.Info.Worktrees
		}
		if len(rows) != 1 || rows[0].Path != external || rows[0].Branch != "external" || rows[0].CreatedAt != nil {
			t.Errorf("query = %+v, want fresh external checkout without an invented creation date", rows)
		}
		if tracked, err := cli.WorktreeList(unknown, 0); err != nil || len(tracked.Worktrees) != 0 {
			t.Fatalf("browsing registered worktrees: %+v, %v", tracked, err)
		}
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
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	repo := newRepo(t, "shop")
	path := createWorktree(t, app, repo, "feature")
	createdAt := sweepCreatedAt(t, app, path)
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
	for _, query := range []any{
		protocol.ListWorktreesMessage{Cmd: protocol.CmdListWorktrees, MainRepo: repo},
		protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: repo},
	} {
		switch query.(type) {
		case protocol.ListWorktreesMessage:
			listed := testworld.Request(app, query, protocol.EventWorktreesUpdated, func(protocol.WebSocketEvent) bool { return true })
			if len(listed.Worktrees) != 0 {
				t.Errorf("query still shows a removed checkout: %+v", listed.Worktrees)
			}
		default:
			info := testworld.Request(app, query, protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
			if !info.Success || info.Info == nil || len(info.Info.Worktrees) != 0 {
				t.Errorf("query still shows a removed checkout: %+v", info)
			}
		}
		if tracked, err := cli.WorktreeList(repo, 0); err != nil || !slices.Equal(worktreeCreatePaths(tracked.Worktrees), []string{path}) {
			t.Fatalf("query changed registry: %+v, %v", tracked, err)
		}
	}
	refreshWorktrees(t, cli)
	testworld.Await(app, protocol.EventWorktreeDeleted, func(e protocol.WebSocketEvent) bool {
		return len(e.Worktrees) == 1 && e.Worktrees[0].Path == path
	})
	if tracked, err := cli.WorktreeList(repo, 0); err != nil || len(tracked.Worktrees) != 0 {
		t.Errorf("sweep left the removed checkout registered: %+v, %v", tracked, err)
	}
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
	testworld.Request(app, protocol.ListWorktreesMessage{Cmd: protocol.CmdListWorktrees, MainRepo: repo},
		protocol.EventWorktreesUpdated, func(protocol.WebSocketEvent) bool { return true })
	testworld.Request(app, protocol.GetRepoInfoMessage{Cmd: protocol.CmdGetRepoInfo, Repo: repo},
		protocol.EventGetRepoInfoResult, func(protocol.GetRepoInfoResultMessage) bool { return true })
	inspection.release(t)
	if swept := sweepAwaitSwept(app, path); swept.Action != "removed" {
		t.Errorf("cleanup after queries = %+v, want the pass to finish removing the eligible checkout", swept)
	}
}

func TestAutomationWorktreeCreationEnrollsItsRepositoryAfterCleanup(t *testing.T) {
	r := newAutomationReviewWorld(t)
	applyAutomation(t, r.cli, automationReviewSpec("manual-review", "manual", automationReviewOverride(r.clone)))
	run, err := r.cli.AutomationRun(2, "monitor", automationReviewInput(42, r.head))
	if err != nil {
		t.Fatal(err)
	}
	session := protocol.Deref(run.Run.SessionID)
	r.w.Launched(string(session))
	if err := r.cli.Unregister(session); err != nil {
		t.Fatal(err)
	}
	cleaned, err := r.cli.AutomationCleanup(2)
	if err != nil || !slices.Equal(cleaned.Cleaned, []string{run.Run.ID}) {
		t.Fatalf("cleanup = %+v, %v, want the last automation checkout removed", cleaned, err)
	}
	r.w.restart()
	r.app, r.cli = r.w.App(), r.w.Client()
	external := filepath.Join(filepath.Dir(r.clone), "external")
	runGit(t, r.clone, "worktree", "add", "-b", "external", external)
	refreshWorktrees(t, r.cli)
	if observed := sweepAwaitState(r.app, external, func(protocol.Worktree) bool { return true }); observed.MainRepo != r.clone {
		t.Errorf("external worktree after automation cleanup and restart = %+v, want repository %s", observed, r.clone)
	}
}
