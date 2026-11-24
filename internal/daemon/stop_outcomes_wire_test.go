package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/github/mockserver"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestStoppingTheDaemonLetsTheLastTitleAttemptFinish(t *testing.T) {
	tasks := make(chan *fakeagent.HeadlessTask, 1)
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) { tasks <- task })
		app := w.App()
		session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
			m.InitialPrompt = protocol.Ptr("fix the flaky checkout test")
		})
		for _, backoff := range []time.Duration{time.Minute, 2 * time.Minute} {
			(<-tasks).Fail("the provider is unavailable")
			awaitTitleTask(app, session, func(task protocol.Task) bool { return task.State == "failed" })
			w.elapse(backoff)
		}
		last := <-tasks
		finish := w.beginStop(t)
		last.Answer("Fix checkout test")
		finish()
		w.start()
		if got := w.sessionLabel(session); got != "Fix checkout test" {
			t.Errorf("after restart the session label is %q, want Fix checkout test", got)
		}
		listed := testworld.Request(w.App(), protocol.TaskListMessage{Cmd: protocol.CmdTaskList},
			protocol.EventTaskListResult, func(protocol.TaskListResultMessage) bool { return true })
		var found bool
		for _, task := range listed.Tasks {
			if task.Kind == "session_title" && task.Subject == session {
				found = true
				if task.State != "done" || task.Attempts != 3 || protocol.Deref(task.LastError) != "" {
					t.Errorf("after restart the title task is %+v, want done after three attempts without a shutdown error", task)
				}
			}
		}
		if !found {
			t.Error("the title task disappeared after restart")
		}
	})
}

func TestStoppingTheDaemonLetsAnAdmittedWorktreeCreationFinish(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	path := repo + "--feat-stop"
	gate := newMaintenanceGitGate(t, "worktree add -b feat-stop "+path)
	// Keep a reader open even if the old daemon cancels Git, so releasing the gate cannot hang.
	release, err := os.OpenFile(gate.released, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	gate.arm(t)
	app.Send(protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: "feat-stop", Path: protocol.Ptr(path)})
	gate.awaitBlocked(t)
	finish := w.beginStop(t)
	gate.release(t)
	finish()
	w.start()
	listed := repoInfoWorktrees(w.App(), repo)
	var registered bool
	for _, worktree := range listed {
		if worktree.Path == path && worktree.Branch == "feat-stop" {
			registered = true
		}
	}
	if !registered {
		t.Errorf("after restart the admitted worktree is absent from %+v", listed)
	}
	if branch := strings.TrimSpace(runGit(t, path, "branch", "--show-current")); branch != "feat-stop" {
		t.Errorf("the completed worktree is on %q, want feat-stop", branch)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Errorf("the worktree did not finish during stop: %v", err)
	}
}

func TestStoppingTheDaemonLetsAnAdmittedCloneFinish(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "origin")
	target := w.Path("checkout")
	gate := newMaintenanceGitGate(t, "clone "+repo+" "+target)
	release, err := os.OpenFile(gate.released, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	gate.arm(t)
	app.Send(protocol.EnsureRepoMessage{Cmd: protocol.CmdEnsureRepo, CloneURL: repo, TargetPath: target})
	gate.awaitBlocked(t)
	finish := w.beginStop(t)
	gate.release(t)
	finish()
	w.start()
	if got, want := strings.TrimSpace(runGit(t, target, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")); got != want {
		t.Errorf("the admitted clone saved %q, want origin's %q", got, want)
	}
	ensured := testworld.Request(w.App(), protocol.EnsureRepoMessage{Cmd: protocol.CmdEnsureRepo, CloneURL: repo, TargetPath: target},
		protocol.EventEnsureRepoResult, func(r protocol.WebSocketEvent) bool { return protocol.Deref(r.TargetPath) == target })
	if !protocol.Deref(ensured.Success) || protocol.Deref(ensured.Cloned) {
		t.Errorf("ensuring the finished clone after restart = %+v, want success using the existing repo", ensured)
	}
}

func TestStoppingTheDaemonLetsAnAdmittedFetchFinish(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	origin := newRepo(t, "origin")
	checkout := w.Path("checkout")
	runGit(t, "", "clone", origin, checkout)
	head := commitFile(t, origin, "change.go", "package change\n")
	gate := newMaintenanceGitGate(t, "fetch --all --prune")
	release, err := os.OpenFile(gate.released, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	gate.arm(t)
	app.Send(protocol.EnsureRepoMessage{Cmd: protocol.CmdEnsureRepo, TargetPath: checkout, CloneURL: origin})
	gate.awaitBlocked(t)
	finish := w.beginStop(t)
	gate.release(t)
	finish()
	w.start()
	if got := strings.TrimSpace(runGit(t, checkout, "rev-parse", "refs/remotes/origin/main")); got != head {
		t.Errorf("the admitted fetch saved %q, want origin's new head %q", got, head)
	}
	fetched := testworld.Request(w.App(), protocol.EnsureRepoMessage{Cmd: protocol.CmdEnsureRepo, TargetPath: checkout, CloneURL: origin},
		protocol.EventEnsureRepoResult, func(protocol.WebSocketEvent) bool { return true })
	if !protocol.Deref(fetched.Success) {
		t.Errorf("fetching again after restart = %+v, want a usable checkout", fetched)
	}
}

func TestStoppingTheDaemonLetsAnAdmittedPRDetailsReadApply(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")

	w := newWorld(t)
	if err := w.Client().ToggleMuteRepo("acme/docs"); err != nil {
		t.Fatalf("mute repo: %v", err)
	}
	app := w.App()
	docs := mockserver.MockPR{Repo: "acme/docs", Number: 3, Title: "Document checkout", Role: "reviewer"}
	gh.AddPR(docs)
	refreshPRs(t, app)
	id := prNumbered(t, queryPRs(t, w.Client(), ""), docs.Number).ID
	fetchPRDetails(t, app, id)
	before := producedBy(busStatus(t, app), "pr.details.changed")

	setPRDetails(gh, docs, "unstable", "", 0)
	held, release := gh.HoldReviews(docs.Repo, docs.Number)
	t.Cleanup(release)
	app.Send(protocol.FetchPRDetailsMessage{Cmd: protocol.CmdFetchPRDetails, ID: id})
	awaitHeldReviews(t, held, id)
	finish := w.beginStop(t)
	release()
	finish()
	w.start()
	assertStoredDetails(t, w.Client(), docs.Number, "pending", "abc123", false)
	if published := producedBy(busStatus(t, w.App()), "pr.details.changed") - before; published != 1 {
		t.Errorf("the read that applied while the daemon stopped published %d pr.details.changed, want 1", published)
	}
}

func TestStoppingTheDaemonLetsAnAdmittedDetailRefreshApplyAndPublish(t *testing.T) {
	gh := mockserver.New()
	t.Cleanup(gh.Close)
	docs := mockserver.MockPR{Repo: "acme/docs", Number: 3, Title: "Document checkout", Role: "reviewer", MergeableState: "unstable"}
	gh.AddPR(docs)
	held, release := gh.HoldReviews(docs.Repo, docs.Number)
	t.Cleanup(release)
	t.Setenv("ATTN_MOCK_GH_URL", gh.URL)
	t.Setenv("ATTN_MOCK_GH_TOKEN", "test-token")

	w := newWorld(t)
	awaitHeldReviews(t, held, "the startup detail refresh")
	if err := w.Client().ToggleMuteRepo(docs.Repo); err != nil {
		t.Fatalf("mute repo: %v", err)
	}
	finish := w.beginStop(t)
	release()
	finish()
	w.start()
	assertStoredDetails(t, w.Client(), docs.Number, "pending", "abc123", false)
	if published := producedBy(busStatus(t, w.App()), "pr.details.changed"); published != 1 {
		t.Errorf("the detail refresh that applied while the daemon stopped published %d pr.details.changed, want 1", published)
	}
}

func awaitHeldReviews(t *testing.T, held <-chan struct{}, what string) {
	t.Helper()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	select {
	case <-held:
	case <-guard.C:
		t.Fatalf("%s never asked GitHub for reviews within fakeagent.HangGuard=%s", what, fakeagent.HangGuard)
	}
}
