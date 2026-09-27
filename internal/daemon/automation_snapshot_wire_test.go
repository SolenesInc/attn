package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAHeldRunStartsAfterARestartOnTheDefinitionAsItWasWhenItFellDue(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "false")
	r := newAutomationReviewWorld(t)
	spec := automationReviewSpec("manual-review", "manual", automationReviewOverride(r.clone))
	applyAutomation(t, r.cli, spec)
	upstream := newRepo(t, "upstream")
	unfetched := commitFile(t, upstream, "later.go", "package later\n")
	if _, err := r.cli.AutomationRun("manual-review", "held", automationReviewInput(46, unfetched)); err == nil {
		t.Fatal("a run whose head cannot be fetched started")
	}
	held := automationRuns(t, r.cli, "manual-review")
	if len(held) != 1 || held[0].State != "pending" {
		t.Fatalf("runs = %+v, want the run held pending", held)
	}

	applyAutomation(t, r.cli, strings.Replace(spec, "Review this pull request.", "Review this pull request for security.", 1))
	runGit(t, r.clone, "fetch", upstream, "main")
	r.w.restart()
	r.app, r.cli = r.w.App(), r.w.Client()
	r.awaitRuns("manual-review", func(runs []protocol.AutomationRunSummary) bool {
		return automationRunState(runs, held[0].ID) == "delivered"
	})

	prompt := r.w.Launched(protocol.Deref(held[0].SessionID)).Prompted()
	if !strings.Contains(prompt, "Review this pull request.") || strings.Contains(prompt, "for security") {
		t.Errorf("the held run started with %q, want the prompt it fell due with", prompt)
	}
}
