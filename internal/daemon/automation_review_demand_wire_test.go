package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestReviewRequestsMadeBeforeTheAutomationWatchedStartNoReviewAcrossARestartReapplyOrReEnable(t *testing.T) {
	r := newAutomationReviewWorld(t, 41)
	r.w.restart()
	r.app, r.cli = r.w.App(), r.w.Client()
	r.refresh()

	applyAutomation(t, r.cli, automationEditSpec(1, automationReviewSpec("review", "github_review_requested", automationReviewOverride(r.clone))))
	r.github.request(42, r.head, false)
	r.refresh()
	first := r.awaitNewRun(1, "delivered")
	if pr := first.Automation.PullRequest; pr == nil || pr.Number != 42 {
		t.Fatalf("the first review ran on %+v, want only #42, requested after the automation began watching", first.Automation)
	}
	r.w.Launched(string(protocol.Deref(first.SessionID)))

	setAutomationEnabled(t, r.cli, 1, false)
	r.github.request(43, r.head, false)
	r.refresh()
	setAutomationEnabled(t, r.cli, 1, true)
	r.refresh()
	r.github.request(44, r.head, false)
	r.refresh()
	after := r.awaitNewRun(1, "delivered", first)
	if pr := after.Automation.PullRequest; pr == nil || pr.Number != 44 {
		t.Fatalf("after re-enabling, the review ran on %+v, want only #44; #43 was requested while the automation was off", after.Automation)
	}
	r.w.Launched(string(protocol.Deref(after.SessionID)))

	r.rerequest(43)
	again := r.awaitNewRun(1, "delivered", first, after)
	if pr := again.Automation.PullRequest; pr == nil || pr.Number != 43 {
		t.Errorf("a fresh request for #43 ran on %+v, want #43", again.Automation)
	}
	r.w.Launched(string(protocol.Deref(again.SessionID)))
}

func TestAPushToAPullRequestUnderReviewStartsAReviewOfTheNewHeadOnEachAutomationsOwnThread(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "false")
	r := newAutomationReviewWorld(t)
	applyAutomation(t, r.cli, automationReviewSpec("second", "github_review_requested", automationReviewOverride(r.clone)))
	r.refresh()
	r.github.request(42, r.head, false)
	r.refresh()
	threads := map[int]protocol.AutomationRunSummary{}
	for _, id := range []int{1, 2} {
		threads[id] = r.awaitNewRun(id, "delivered")
		r.w.Launched(string(protocol.Deref(threads[id].SessionID)))
	}
	if protocol.Deref(threads[1].SeedID) == protocol.Deref(threads[2].SeedID) {
		t.Fatalf("both automations reviewed #42 on seed %s, want a thread each", protocol.Deref(threads[1].SeedID))
	}

	pushed := commitFile(t, r.clone, "fix.go", "package fix\n")
	r.github.request(42, pushed, false)
	r.readPullRequest(42, pushed)
	r.refresh()
	for id, first := range threads {
		next := r.awaitNewRun(id, "delivered", first)
		if protocol.Deref(next.SeedID) != protocol.Deref(first.SeedID) || protocol.Deref(next.SessionID) != protocol.Deref(first.SessionID) {
			t.Errorf("%d reviewed the push on %s/%s, want its thread %s/%s", id, protocol.Deref(next.SeedID), protocol.Deref(next.SessionID), protocol.Deref(first.SeedID), protocol.Deref(first.SessionID))
		}
		if pr := next.Automation.PullRequest; pr == nil || pr.HeadSHA != pushed {
			t.Errorf("%d reviewed %+v after the push, want head %s", id, next.Automation, pushed)
		}
	}

	earlier := automationRuns(t, r.cli, 1)
	r.w.restart()
	r.app, r.cli = r.w.App(), r.w.Client()
	r.refresh()
	upstream := newRepo(t, "upstream")
	held := commitFile(t, upstream, "held.go", "package held\n")
	r.github.request(42, held, false)
	r.readPullRequest(42, held)
	r.refresh()
	pending := r.awaitNewRun(1, "pending", earlier...)
	newer := commitFile(t, upstream, "newer.go", "package newer\n")
	r.github.request(42, newer, false)
	r.readPullRequest(42, newer)
	r.refresh()
	r.refresh()
	if runs := automationRuns(t, r.cli, 1); len(runs) != len(earlier)+1 || automationRunState(runs, pending.ID) != "pending" {
		t.Fatalf("a push while the review of %s waits = runs %+v, want that run still the one pending", held, runs)
	}
	runGit(t, r.clone, "fetch", upstream, "main")
	r.refresh()
	r.awaitRuns(1, func(runs []protocol.AutomationRunSummary) bool {
		return automationRunState(runs, pending.ID) == "delivered"
	})
	r.refresh()
	latest := r.awaitNewRun(1, "delivered", append(earlier, pending)...)
	if pr := latest.Automation.PullRequest; pr == nil || pr.HeadSHA != newer {
		t.Errorf("after the waiting review started, the next review ran on %+v, want the newest head %s", latest.Automation, newer)
	}
}

func (r *automationReviewWorld) readPullRequest(number int, head string) {
	r.t.Helper()
	// Detail reads reuse fresh cached heads; refresh the list before reading a simulated push.
	r.refresh()
	result := testworld.Request(r.app, protocol.FetchPRDetailsMessage{Cmd: protocol.CmdFetchPRDetails, ID: protocol.FormatPRID("github.test", "acme/shop", number)},
		protocol.EventFetchPRDetailsResult, func(protocol.FetchPRDetailsResultMessage) bool { return true })
	if !result.Success {
		r.t.Fatalf("fetch pull request #%d: %s", number, protocol.Deref(result.Error))
	}
	pr := prNumbered(r.t, result.Prs, number)
	if got := protocol.Deref(pr.HeadSHA); got != head {
		r.t.Fatalf("pull request #%d head = %s, want %s", number, got, head)
	}
}
