package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestStoppingTheDaemonLetsTheLastGardenAdviceAttemptFinish(t *testing.T) {
	tasks := make(chan *fakeagent.HeadlessTask, 1)
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) { tasks <- task })
		app, cli := w.App(), w.Client()
		setSetting(t, app, "garden.advisor", `{"agent":"claude"}`)
		seed := gardenReviewRegisteredAbandonedSeed(t, w, cli, "gardener", "unfinished work")
		review := gardenReviewStart(t, cli)
		for _, backoff := range []time.Duration{time.Minute, 2 * time.Minute} {
			(<-tasks).Fail("the provider is unavailable")
			gardenReviewAwaitFailedFirstAdvice(app, review.Run.ID)
			w.elapse(backoff)
		}
		last := <-tasks
		item := gardenReviewItem(t, gardenReviewShow(t, cli, review.Run.ID).Review, seed)
		if protocol.Deref(item.AdvisorAttempt) != protocol.Deref(item.AdvisorMaxAttempts) || protocol.Deref(item.AdvisorAttempt) != 3 {
			t.Fatalf("before stop the advice is on attempt %d of %d, want the last attempt", protocol.Deref(item.AdvisorAttempt), protocol.Deref(item.AdvisorMaxAttempts))
		}
		finish := w.beginStop(t)
		last.Answer(`{"recommendation":"keep_growing","explanation":"Work remains.","evidence":["The seed is unfinished."]}`)
		finish()
		w.start()
		item = gardenReviewItem(t, gardenReviewShow(t, w.Client(), review.Run.ID).Review, seed)
		if item.Status != "ready" || protocol.Deref(item.Recommendation) != "keep_growing" || protocol.Deref(item.Error) != "" {
			t.Errorf("after restart the last advice attempt is %+v, want genuine advice without a shutdown failure", item)
		}
		listed := testworld.Request(w.App(), protocol.TaskListMessage{Cmd: protocol.CmdTaskList},
			protocol.EventTaskListResult, func(protocol.TaskListResultMessage) bool { return true })
		var found bool
		for _, task := range listed.Tasks {
			if task.Kind == "garden_review_classify" && task.Subject == item.ID {
				found = true
				if task.State != "done" || task.Attempts != 3 || protocol.Deref(task.LastError) != "" {
					t.Errorf("after restart the advice task is %+v, want done after three attempts without a shutdown error", task)
				}
			}
		}
		if !found {
			t.Error("the advice task disappeared after restart")
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
	listed := testworld.Request(w.App(), protocol.ListWorktreesMessage{Cmd: protocol.CmdListWorktrees, MainRepo: repo},
		protocol.EventWorktreesUpdated, func(protocol.WebSocketEvent) bool { return true }).Worktrees
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
	app.Send(protocol.FetchRemotesMessage{Cmd: protocol.CmdFetchRemotes, Repo: checkout})
	gate.awaitBlocked(t)
	finish := w.beginStop(t)
	gate.release(t)
	finish()
	w.start()
	if got := strings.TrimSpace(runGit(t, checkout, "rev-parse", "refs/remotes/origin/main")); got != head {
		t.Errorf("the admitted fetch saved %q, want origin's new head %q", got, head)
	}
	fetched := testworld.Request(w.App(), protocol.FetchRemotesMessage{Cmd: protocol.CmdFetchRemotes, Repo: checkout},
		protocol.EventFetchRemotesResult, func(protocol.WebSocketEvent) bool { return true })
	if !protocol.Deref(fetched.Success) {
		t.Errorf("fetching again after restart = %+v, want a usable checkout", fetched)
	}
}
