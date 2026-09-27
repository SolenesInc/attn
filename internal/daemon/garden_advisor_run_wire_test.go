package daemon_test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheGardenAdvisorAdvisesAndDraftsWithTheReviewsFrozenRecipeFromBoundedEvidence(t *testing.T) {
	w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	repo := w.Path("repo")
	gitRepo(t, repo)
	predecessor, err := cli.Delegate(delegateCheckoutAt(filepath.Join(repo, "web"), delegateNewWorktree("feature/checkout", "main")))
	if err != nil {
		t.Fatal(err)
	}
	w.Launched(predecessor.SessionID)
	closePane(app, sessionPane{session: predecessor.SessionID})
	worktreeRoot := filepath.Dir(predecessor.Directory)
	if deleted := testworld.Request(app, protocol.DeleteWorktreeMessage{Cmd: protocol.CmdDeleteWorktree, Path: worktreeRoot, Force: protocol.Ptr(true)},
		protocol.EventDeleteWorktreeResult, func(r protocol.DeleteWorktreeResultMessage) bool { return r.Path == worktreeRoot }); !deleted.Success {
		t.Fatalf("deleting %s: %s", worktreeRoot, protocol.Deref(deleted.Error))
	}
	seed := predecessor.SeedID
	if _, err := cli.SeedEdit(seed, strings.Repeat("b", 20000)); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.SeedNote("", seed, strings.Repeat("n", 2000), "", "", false, nil); err != nil {
		t.Fatal(err)
	}

	setSetting(t, app, "garden.advisor", `{"agent":"claude"}`)
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	review := gardenReviewStart(t, cli)
	if len(review.Items) != 1 || review.Items[0].SeedID != seed || !slices.Contains(review.Items[0].Actions, "handover") {
		t.Fatalf("the review = %+v, want the abandoned seed with a handover to offer", review.Items)
	}
	setSetting(t, app, "garden.advisor", `{"agent":"codex","model":"later","effort":"low"}`)
	frozen := func(task *fakeagent.HeadlessTask, what string) {
		t.Helper()
		if task.Harness != fakeagent.Claude || task.Model != "sonnet" || task.Effort != "medium" {
			t.Errorf("the %s ran %s %q at effort %q, want the review's frozen Claude sonnet at medium", what, task.Harness, task.Model, task.Effort)
		}
	}
	task := w.HeadlessTask()
	frozen(task, "advice")
	if !strings.Contains(task.Prompt, strings.Repeat("b", 15000)) || strings.Contains(task.Prompt, strings.Repeat("b", 16000)) ||
		!strings.Contains(task.Prompt, strings.Repeat("n", 1000)) || strings.Contains(task.Prompt, strings.Repeat("n", 1200)) {
		t.Errorf("the advisor was shown %d body and %d log characters, want both cut to their caps", strings.Count(task.Prompt, "b"), strings.Count(task.Prompt, "n"))
	}

	finish := w.beginStop(t)
	task.Answer(`{"recommendation":"harvest","explanation":" The branch holds the finished work. ","evidence":[" The seed log records the work. "]}`)
	finish()
	w.start()
	app = w.App()
	ready := gardenReviewItem(t, gardenReviewShow(t, w.Client(), review.Run.ID).Review, seed)
	if protocol.Deref(ready.Recommendation) != "harvest" || protocol.Deref(ready.Explanation) != "The branch holds the finished work." ||
		!slices.Equal(ready.CitedEvidence, []string{"The seed log records the work."}) {
		t.Errorf("the advised item = %q because %q citing %q, want the advisor's trimmed harvest", protocol.Deref(ready.Recommendation), protocol.Deref(ready.Explanation), ready.CitedEvidence)
	}

	drafted := make(chan protocol.SeedReviewDraftResultMessage, 1)
	go func() {
		drafted <- testworld.Request(app, protocol.SeedReviewDraftMessage{Cmd: protocol.CmdSeedReviewDraft, RequestID: "draft", SeedID: seed,
			Review: protocol.SeedReviewActionContext{ReviewID: review.Run.ID, EvidenceVersion: ready.EvidenceVersion}},
			protocol.EventSeedReviewDraftResult, func(m protocol.SeedReviewDraftResultMessage) bool { return m.RequestID == "draft" })
	}()
	task = w.HeadlessTask()
	frozen(task, "handoff draft")
	task.Answer(`{"handoff":"  Continue from the failing integration test.  "}`)
	if draft := <-drafted; !draft.Success || protocol.Deref(draft.Handoff) != "Continue from the failing integration test." {
		t.Errorf("the handoff draft = %+v, want the advisor's trimmed handoff", draft)
	}
}

func TestEachReviewItemKeepsOnlyAdviceItCouldActOnForTheEvidenceItWasGiven(t *testing.T) {
	w := newWorld(t, fakeagent.Claude, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	answers := map[string]string{
		"keep":      `{"recommendation":"keep_growing","explanation":"The maintainer is still deciding.","evidence":["The seed log asks for a later decision."]}`,
		"prose":     "Harvest it, the work is done.",
		"invented":  `{"recommendation":"delete","explanation":"No.","evidence":["guess"]}`,
		"extra":     `{"recommendation":"park","explanation":"Later.","evidence":["age"],"confidence":"high"}`,
		"blank":     `{"recommendation":"park","explanation":"Later.","evidence":[" "]}`,
		"unoffered": `{"recommendation":"resume","explanation":"Continue it.","evidence":["saved conversation"]}`,
		"changed":   `{"recommendation":"harvest","explanation":"Done.","evidence":["Old evidence."]}`,
	}
	seeds := map[string]string{}
	for name := range answers {
		seeds[gardenReviewAbandonedSeed(t, w, app, cli, name, name+" work")] = name
	}
	t.Setenv("ATTN_HEADLESS_TASKS", "on")
	review := gardenReviewStart(t, cli)

	seedID := regexp.MustCompile(`"seed_id": "([^"]+)"`)
	for range answers {
		task := w.HeadlessTask()
		match := seedID.FindStringSubmatch(task.Prompt)
		if task.Harness != fakeagent.Codex || match == nil || seeds[match[1]] == "" {
			t.Fatalf("the daemon asked %s %q, want Codex advising one of the review's seeds", task.Harness, task.Prompt)
		}
		name := seeds[match[1]]
		if name == "changed" {
			if _, err := cli.SeedEdit(match[1], "New work arrived while the advisor was reading."); err != nil {
				t.Fatal(err)
			}
		}
		task.Answer(answers[name])
	}
	settled := testworld.Await(app, protocol.EventGardenReviewUpdated, func(m protocol.GardenReviewUpdatedMessage) bool {
		if m.Review.Run.ID != review.Run.ID {
			return false
		}
		for _, item := range m.Review.Items {
			if item.Status != "ready" && item.Status != "invalidated" && protocol.Deref(item.AdvisorState) != "retrying" {
				return false
			}
		}
		return true
	}).Review

	failures := map[string]string{}
	requestID := uuid.NewString()
	for _, task := range testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
		protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID }).Tasks {
		if task.Kind == "garden_review_classify" {
			failures[task.Subject] = protocol.Deref(task.LastError)
		}
	}
	for _, item := range settled.Items {
		name, recommendation, failure := seeds[item.SeedID], protocol.Deref(item.Recommendation), failures[item.ID]
		want := map[string]string{
			"prose": "invalid advice", "invented": "invalid advice", "extra": "invalid advice",
			"blank": "empty evidence", "unoffered": `unavailable action "resume"`,
		}[name]
		switch name {
		case "keep":
			if item.Status != "ready" || recommendation != "keep_growing" {
				t.Errorf("%s is %s recommending %q, want ready to keep growing", name, item.Status, recommendation)
			}
		case "changed":
			if item.Status != "invalidated" || recommendation != "" {
				t.Errorf("%s is %s recommending %q, want its advice on the old evidence discarded", name, item.Status, recommendation)
			}
		default:
			if recommendation != "" || protocol.Deref(item.AdvisorState) != "retrying" || !strings.Contains(failure, want) {
				t.Errorf("%s recommends %q, advisor %s after %q; want the advice refused for %s and retried", name, recommendation, protocol.Deref(item.AdvisorState), failure, want)
			}
		}
	}
}
