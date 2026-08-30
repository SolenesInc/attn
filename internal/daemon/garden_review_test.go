package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
)

func TestStartGardenReviewFreezesCandidatesAndRecipeAndDeduplicates(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	d.store.SetSetting(SettingGardenAdvisor, `{"agent":"claude","model":"sonnet","effort":"medium"}`)
	installGardenReviewRunner(t, d, false)

	first, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	if first.Status != garden.ReviewRunStatusRunning || len(items) != 1 || items[0].SeedID != seed.ID {
		t.Fatalf("first review = %+v items=%+v", first, items)
	}
	wantActions := []string{"keep_growing", "park", "harvest", "wither"}
	if !slices.Equal(items[0].Actions, wantActions) {
		t.Fatalf("actions = %v, want %v", items[0].Actions, wantActions)
	}
	if first.Recipe != (garden.ReviewRecipe{Agent: "claude", Model: "sonnet", Effort: "medium"}) {
		t.Fatalf("frozen recipe = %+v", first.Recipe)
	}

	d.store.SetSetting(SettingGardenAdvisor, `{"agent":"codex","model":"later","effort":"low"}`)
	second, secondItems, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("duplicate startGardenReview: %v", err)
	}
	if second.ID != first.ID || second.Recipe != first.Recipe || len(secondItems) != 1 {
		t.Fatalf("duplicate review = %+v items=%+v, want existing %s", second, secondItems, first.ID)
	}
	job, err := d.jobQueue.GetByKey(gardenReviewClassifyKind, items[0].ID)
	if err != nil || job == nil {
		t.Fatalf("classification job = %+v error=%v", job, err)
	}
}

func TestGardenReviewOffersChiefWithoutAReconstructableHandover(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	addGardenSession(t, d, "chief")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	capture, err := d.captureGardenReview()
	if err != nil {
		t.Fatalf("captureGardenReview: %v", err)
	}
	want := []string{"send_to_chief", "keep_growing", "park", "harvest", "wither"}
	if got := capture.items[seed.ID].Actions; !slices.Equal(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
}

func TestGardenReviewOffersResumeOnlyWithUsableContinuation(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-garden.DefaultStaleWindow)
	d.gardenNow = func() time.Time { return old }
	cwd := t.TempDir()
	d.store.Remove("sess-a")
	d.store.Add(&protocol.Session{
		ID: "sess-a", Directory: cwd, Agent: protocol.SessionAgentCopilot, State: protocol.SessionStateIdle,
	})
	d.store.SetResumeSessionID("sess-a", "native-1")
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Resumable old work"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	d.store.Remove("sess-a")
	d.gardenNow = func() time.Time { return now }

	capture, err := d.captureGardenReview()
	if err != nil {
		t.Fatalf("captureGardenReview: %v", err)
	}
	item := capture.items[seed.ID]
	if !slices.Equal(item.Actions, []string{"resume", "handover", "keep_growing", "park", "harvest", "wither"}) {
		t.Fatalf("resumable actions = %v", item.Actions)
	}
}

func TestGardenReviewProtectsTrackedRecoverableAndPermanentMemberClaims(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	recoverable := oldUnheldGrowingSeed(t, d, now)
	d.store.Add(&protocol.Session{ID: "sess-a", State: protocol.SessionStateRecoverable})

	old := now.Add(-garden.DefaultStaleWindow)
	d.gardenNow = func() time.Time { return old }
	memberSeed := plant(t, d, protocol.SeedPlantMessage{Title: "Member-owned old work"})
	move(t, d, "", memberSeed.ID, garden.VerbTend, "", "trellis")
	d.gardenNow = func() time.Time { return now }

	capture, err := d.captureGardenReview()
	if err != nil {
		t.Fatalf("captureGardenReview: %v", err)
	}
	if _, found := capture.items[recoverable.ID]; found {
		t.Fatalf("tracked recoverable claim %s became a candidate", recoverable.ID)
	}
	if _, found := capture.items[memberSeed.ID]; found {
		t.Fatalf("permanent member claim %s became a candidate", memberSeed.ID)
	}
}

func TestGardenReviewJobUsesFrozenRecipeAndCompletesProgressively(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	oldUnheldGrowingSeed(t, d, now)
	d.store.SetSetting(SettingGardenAdvisor, `{"agent":"claude","model":"frozen-sonnet","effort":"high"}`)
	installGardenReviewRunner(t, d, false)

	var got gardenAdvisorConfig
	d.gardenAdvisorResolve = func(config gardenAdvisorConfig) (agentdriver.HeadlessTaskProvider, string, error) {
		got = config
		return gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{StructuredOutput: json.RawMessage(
				`{"recommendation":"harvest","explanation":"The stated work and verification are complete.","evidence":["The seed log records the passing verification."]}`,
			)}, nil
		}), "/fake/claude", nil
	}
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	d.store.SetSetting(SettingGardenAdvisor, `{"agent":"codex","model":"changed","effort":"low"}`)
	payload := gardenReviewJobPayload{RunID: run.ID, ItemID: items[0].ID, EvidenceVersion: items[0].EvidenceVersion}
	raw, _ := json.Marshal(payload)
	job := &jobs.Job{Payload: raw, CommitGuard: &jobs.CommitGuard{}}

	if _, err := d.gardenReviewClassifyHandler(t.Context(), job); err != nil {
		t.Fatalf("gardenReviewClassifyHandler: %v", err)
	}
	if got != (gardenAdvisorConfig{Agent: "claude", Model: "frozen-sonnet", Effort: "high"}) {
		t.Fatalf("advisor recipe = %+v, want frozen run recipe", got)
	}
	completed, completedItems, err := d.showGardenReview(run.ID)
	if err != nil {
		t.Fatalf("showGardenReview: %v", err)
	}
	if completed.Status != garden.ReviewRunStatusRunning || len(completedItems) != 1 {
		t.Fatalf("classified review = %+v items=%+v", completed, completedItems)
	}
	item := completedItems[0]
	if item.Status != garden.ReviewItemStatusReady || item.Recommendation != "harvest" ||
		item.Resolution != garden.ReviewResolutionUnresolved {
		t.Fatalf("completed item = %+v", item)
	}
	continued, _, err := d.startGardenReview()
	if err != nil || continued.ID != run.ID {
		t.Fatalf("start after classification = %+v err=%v, want current review %s", continued, err, run.ID)
	}
}

func TestGardenReviewFailureDoesNotBlockOtherItems(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	oldUnheldGrowingSeed(t, d, now)
	addGardenSession(t, d, "sess-a")
	oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	d.gardenAdvisorResolve = func(gardenAdvisorConfig) (agentdriver.HeadlessTaskProvider, string, error) {
		return gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{StructuredOutput: json.RawMessage(
				`{"recommendation":"park","explanation":"Keep it for later.","evidence":["Work remains."]}`,
			)}, nil
		}), "/fake/codex", nil
	}
	run, items, err := d.startGardenReview()
	if err != nil || len(items) != 2 {
		t.Fatalf("startGardenReview = %+v items=%+v err=%v", run, items, err)
	}
	failedPayload, _ := json.Marshal(gardenReviewJobPayload{
		RunID: run.ID, ItemID: items[0].ID, EvidenceVersion: items[0].EvidenceVersion,
	})
	d.failGardenReviewJob(&jobs.Job{
		Kind: gardenReviewClassifyKind, Payload: failedPayload, LastError: "provider unavailable",
	})
	stillRunning, _, err := d.showGardenReview(run.ID)
	if err != nil || stillRunning.Status != garden.ReviewRunStatusRunning {
		t.Fatalf("run after one failure = %+v err=%v", stillRunning, err)
	}

	successPayload, _ := json.Marshal(gardenReviewJobPayload{
		RunID: run.ID, ItemID: items[1].ID, EvidenceVersion: items[1].EvidenceVersion,
	})
	if _, err := d.gardenReviewClassifyHandler(t.Context(), &jobs.Job{
		Payload: successPayload, CommitGuard: &jobs.CommitGuard{},
	}); err != nil {
		t.Fatalf("classify second item: %v", err)
	}
	complete, completedItems, err := d.showGardenReview(run.ID)
	if err != nil || complete.Status != garden.ReviewRunStatusRunning {
		t.Fatalf("classified run = %+v err=%v", complete, err)
	}
	statuses := []string{completedItems[0].Status, completedItems[1].Status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []string{garden.ReviewItemStatusFailed, garden.ReviewItemStatusReady}) {
		t.Fatalf("item statuses = %v", statuses)
	}
}

func TestGardenReviewConcurrentItemCompletionPersistsBothItems(t *testing.T) {
	d := newGardenDaemon(t)
	run := garden.ReviewRun{
		ID: "r-concurrent", CandidateIDs: []string{"s-one", "s-two"},
		Recipe: garden.ReviewRecipe{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"},
		Status: garden.ReviewRunStatusRunning, CapturedAt: formatGardenTime(time.Now()),
	}
	items := []garden.ReviewItem{
		{ID: garden.ReviewItemID(run.ID, "s-one"), RunID: run.ID, SeedID: "s-one", EvidenceVersion: "e-one", Status: garden.ReviewItemStatusQueued, Resolution: garden.ReviewResolutionUnresolved, Evidence: []garden.ReviewEvidence{}, Actions: []string{}},
		{ID: garden.ReviewItemID(run.ID, "s-two"), RunID: run.ID, SeedID: "s-two", EvidenceVersion: "e-two", Status: garden.ReviewItemStatusQueued, Resolution: garden.ReviewResolutionUnresolved, Evidence: []garden.ReviewEvidence{}, Actions: []string{}},
	}
	if err := d.createGardenReview(run, items); err != nil {
		t.Fatalf("createGardenReview: %v", err)
	}

	start := make(chan struct{})
	errors := make(chan error, len(items))
	var workers sync.WaitGroup
	for _, item := range items {
		item := item
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			next := item
			next.Status = garden.ReviewItemStatusReady
			next.CompletedAt = formatGardenTime(time.Now())
			errors <- d.finishGardenReviewItem(gardenReviewJobPayload{
				RunID: run.ID, ItemID: item.ID, EvidenceVersion: item.EvidenceVersion,
			}, next)
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("finishGardenReviewItem: %v", err)
		}
	}
	completed, completedItems, err := d.showGardenReview(run.ID)
	if err != nil || completed.Status != garden.ReviewRunStatusRunning || len(completedItems) != 2 ||
		completedItems[0].Status != garden.ReviewItemStatusReady || completedItems[1].Status != garden.ReviewItemStatusReady {
		t.Fatalf("concurrent completion = %+v items=%+v err=%v", completed, completedItems, err)
	}
}

func TestGardenReviewInvalidatesAdviceWhenEvidenceChangesDuringClassification(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	started := make(chan struct{})
	continueRun := make(chan struct{})
	d.gardenAdvisorResolve = func(config gardenAdvisorConfig) (agentdriver.HeadlessTaskProvider, string, error) {
		return gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			close(started)
			<-continueRun
			return agentdriver.HeadlessTaskResult{StructuredOutput: json.RawMessage(
				`{"recommendation":"harvest","explanation":"Done.","evidence":["Old evidence."]}`,
			)}, nil
		}), "/fake/codex", nil
	}
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	payload, _ := json.Marshal(gardenReviewJobPayload{
		RunID: run.ID, ItemID: items[0].ID, EvidenceVersion: items[0].EvidenceVersion,
	})
	job := &jobs.Job{Payload: payload, CommitGuard: &jobs.CommitGuard{}}
	errCh := make(chan error, 1)
	go func() {
		_, handlerErr := d.gardenReviewClassifyHandler(context.Background(), job)
		errCh <- handlerErr
	}()
	<-started
	editSeed(t, d, seed.ID, "New work arrived while classification was running.")
	close(continueRun)
	if err := <-errCh; err != nil {
		t.Fatalf("gardenReviewClassifyHandler: %v", err)
	}

	completed, completedItems, err := d.showGardenReview(run.ID)
	if err != nil {
		t.Fatalf("showGardenReview: %v", err)
	}
	if completed.Status != garden.ReviewRunStatusRunning ||
		completedItems[0].Status != garden.ReviewItemStatusInvalidated ||
		completedItems[0].Recommendation != "" {
		t.Fatalf("invalidated review = %+v items=%+v", completed, completedItems)
	}
}

func TestGardenReviewLifecycleActionCanResolveBeforeAdviceArrives(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	item := items[0]
	review := &protocol.SeedReviewActionContext{ReviewID: run.ID, EvidenceVersion: item.EvidenceVersion}
	if job, getErr := d.jobQueue.GetByKey(gardenReviewClassifyKind, item.ID); getErr != nil || job == nil {
		t.Fatalf("queued classification job = %+v error=%v", job, getErr)
	}

	client := newInternalWSClient()
	d.handleSeedTransitionWS(client, &protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, RequestID: protocol.Ptr("move-1"),
		SeedID: seed.ID, Verb: string(garden.VerbHarvest),
		Reason: protocol.Ptr("The outcome and verification are complete."), Review: review,
	})
	var result protocol.SeedTransitionResultMessage
	if err := json.Unmarshal((<-client.send).payload, &result); err != nil {
		t.Fatalf("decode transition result: %v", err)
	}
	if !result.Success {
		t.Fatalf("transition result = %+v", result)
	}
	completed, completedItems, err := d.showGardenReview(run.ID)
	if err != nil {
		t.Fatalf("showGardenReview: %v", err)
	}
	if completed.Status != garden.ReviewRunStatusComplete ||
		completedItems[0].Resolution != garden.ReviewResolutionResolved {
		t.Fatalf("completed review = %+v items=%+v", completed, completedItems)
	}
	if job, getErr := d.jobQueue.GetByKey(gardenReviewClassifyKind, item.ID); getErr != nil || job != nil {
		t.Fatalf("classification job after action = %+v error=%v", job, getErr)
	}
}

func TestGardenReviewKeepLeavesSeedGrowingAndWaitsSevenDays(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	item := items[0]
	review := protocol.SeedReviewActionContext{ReviewID: run.ID, EvidenceVersion: item.EvidenceVersion}

	completed, completedItems, err := d.keepGardenReviewItem(review, seed.ID)
	if err != nil {
		t.Fatalf("keepGardenReviewItem: %v", err)
	}
	if completed.Status != garden.ReviewRunStatusComplete ||
		completedItems[0].ResolvedAction != "keep_growing" ||
		completedItems[0].ReviewAgainAt != formatGardenTime(now.Add(garden.DefaultStaleWindow)) {
		t.Fatalf("kept review = %+v items=%+v", completed, completedItems)
	}
	current, _, err := d.readSeed(seed.ID)
	if err != nil || current.Status != garden.StatusGrowing {
		t.Fatalf("kept seed = %+v err=%v", current, err)
	}
	if _, _, count, err := d.gardenReviewOverview(); err != nil || count != 0 {
		t.Fatalf("overview before review window = %d err=%v", count, err)
	}
	d.gardenNow = func() time.Time { return now.Add(garden.DefaultStaleWindow) }
	if _, _, count, err := d.gardenReviewOverview(); err != nil || count != 1 {
		t.Fatalf("overview at review window = %d err=%v", count, err)
	}
}

func TestGardenReviewLifecycleActionRefusesChangedEvidence(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	item := readyGardenReviewItem(t, d, run, items[0])
	editSeed(t, d, seed.ID, "More work arrived after the advice.")

	client := newInternalWSClient()
	d.handleSeedTransitionWS(client, &protocol.SeedTransitionMessage{
		Cmd: protocol.CmdSeedTransition, RequestID: protocol.Ptr("move-stale"),
		SeedID: seed.ID, Verb: string(garden.VerbHarvest),
		Reason: protocol.Ptr("Done."),
		Review: &protocol.SeedReviewActionContext{ReviewID: run.ID, EvidenceVersion: item.EvidenceVersion},
	})
	var result protocol.SeedTransitionResultMessage
	if err := json.Unmarshal((<-client.send).payload, &result); err != nil {
		t.Fatalf("decode transition result: %v", err)
	}
	if result.Success || result.Error == nil {
		t.Fatalf("stale transition result = %+v", result)
	}
	current, _, err := d.readSeed(seed.ID)
	if err != nil || current.Status != garden.StatusGrowing {
		t.Fatalf("seed after stale action = %+v err=%v", current, err)
	}
	_, currentItems, err := d.showGardenReview(run.ID)
	if err != nil || currentItems[0].Resolution != garden.ReviewResolutionUnresolved {
		t.Fatalf("review after stale action = %+v err=%v", currentItems, err)
	}
}

func TestGardenReviewHandoffDraftUsesTheFrozenRecipe(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	if err := d.recordGardenDispatch("sess-a", seed.ID, "", t.TempDir(), "codex", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}
	d.store.SetSetting(SettingGardenAdvisor, `{"agent":"claude","model":"frozen-sonnet","effort":"high"}`)
	installGardenReviewRunner(t, d, false)
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	item := readyGardenReviewItem(t, d, run, items[0])
	d.store.SetSetting(SettingGardenAdvisor, `{"agent":"codex","model":"changed","effort":"low"}`)
	var got gardenAdvisorConfig
	d.gardenAdvisorResolve = func(config gardenAdvisorConfig) (agentdriver.HeadlessTaskProvider, string, error) {
		got = config
		return gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return agentdriver.HeadlessTaskResult{StructuredOutput: json.RawMessage(`{"handoff":"Check the remaining edge and run the focused verification."}`)}, nil
		}), "/fake/claude", nil
	}

	client := newInternalWSClient()
	d.handleSeedReviewDraftWS(client, &protocol.SeedReviewDraftMessage{
		Cmd: protocol.CmdSeedReviewDraft, RequestID: "draft-1", SeedID: seed.ID,
		Review: protocol.SeedReviewActionContext{ReviewID: run.ID, EvidenceVersion: item.EvidenceVersion},
	})
	var result protocol.SeedReviewDraftResultMessage
	if err := json.Unmarshal((<-client.send).payload, &result); err != nil {
		t.Fatalf("decode draft result: %v", err)
	}
	if !result.Success || protocol.Deref(result.Handoff) == "" {
		t.Fatalf("draft result = %+v", result)
	}
	want := gardenAdvisorConfig{Agent: "claude", Model: "frozen-sonnet", Effort: "high"}
	if got != want {
		t.Fatalf("draft config = %+v, want %+v", got, want)
	}
}

func TestCancelGardenReviewPersistsAndRemovesQueuedJobs(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}

	canceled, _, err := d.cancelGardenReview(run.ID)
	if err != nil {
		t.Fatalf("cancelGardenReview: %v", err)
	}
	if canceled.Status != garden.ReviewRunStatusCanceled {
		t.Fatalf("canceled run = %+v", canceled)
	}
	job, err := d.jobQueue.GetByKey(gardenReviewClassifyKind, items[0].ID)
	if err != nil {
		t.Fatalf("read canceled job: %v", err)
	}
	if job != nil {
		t.Fatalf("canceled job still exists: %+v", job)
	}
}

func TestFailedGardenReviewItemCanRetryWithFreshEvidence(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	payload, _ := json.Marshal(gardenReviewJobPayload{
		RunID: run.ID, ItemID: items[0].ID, EvidenceVersion: items[0].EvidenceVersion,
	})
	d.failGardenReviewJob(&jobs.Job{
		Kind: gardenReviewClassifyKind, Payload: payload, LastError: "provider unavailable",
	})
	failed, failedItems, err := d.showGardenReview(run.ID)
	if err != nil {
		t.Fatalf("show failed review: %v", err)
	}
	if failed.Status != garden.ReviewRunStatusRunning || failedItems[0].Status != garden.ReviewItemStatusFailed {
		t.Fatalf("failed review = %+v items=%+v", failed, failedItems)
	}

	editSeed(t, d, seed.ID, "Use this newer body for the retry.")
	retriedRun, retried, err := d.retryGardenReviewItem(run.ID, seed.ID)
	if err != nil {
		t.Fatalf("retryGardenReviewItem: %v", err)
	}
	if retriedRun.Status != garden.ReviewRunStatusRunning || retried.Status != garden.ReviewItemStatusQueued ||
		retried.EvidenceVersion == items[0].EvidenceVersion {
		t.Fatalf("retried run = %+v item=%+v", retriedRun, retried)
	}
}

func TestRetryResolvesAnItemThatNoLongerNeedsReview(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	payload, _ := json.Marshal(gardenReviewJobPayload{
		RunID: run.ID, ItemID: items[0].ID, EvidenceVersion: items[0].EvidenceVersion,
	})
	d.failGardenReviewJob(&jobs.Job{
		Kind: gardenReviewClassifyKind, Payload: payload, LastError: "provider unavailable",
	})
	addGardenSession(t, d, "sess-a")
	move(t, d, "sess-a", seed.ID, garden.VerbHarvest, "The work is complete.", "")

	resolvedRun, resolved, err := d.retryGardenReviewItem(run.ID, seed.ID)
	if err != nil {
		t.Fatalf("retryGardenReviewItem: %v", err)
	}
	if resolvedRun.Status != garden.ReviewRunStatusComplete ||
		resolved.Resolution != garden.ReviewResolutionNoLongerApplicable {
		t.Fatalf("resolved run = %+v item=%+v", resolvedRun, resolved)
	}
}

func TestGardenReviewCapturePagesPastTheGardenSnapshotLimit(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	d.gardenNow = func() time.Time { return now.Add(-garden.DefaultStaleWindow) }
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seedsCollection: %v", err)
	}
	for i := 0; i < docstore.MaxLimit+1; i++ {
		id := fmt.Sprintf("s-%06x", i)
		seed := garden.Seed{
			ID: id, Title: id, Status: garden.StatusGrowing, StepSlug: id,
			StateChangedAt: formatGardenTime(d.gardenNow()), Edges: []garden.Edge{}, Vars: []garden.Var{},
		}
		body, _ := seed.Encode()
		if _, err := d.store.PutDocument(*schema, id, body, d.gardenNow(), nil); err != nil {
			t.Fatalf("put seed %s: %v", id, err)
		}
	}
	d.gardenNow = func() time.Time { return now }
	capture, err := d.captureGardenReview()
	if err != nil {
		t.Fatalf("captureGardenReview: %v", err)
	}
	if len(capture.candidates) != docstore.MaxLimit+1 {
		t.Fatalf("candidates = %d, want %d", len(capture.candidates), docstore.MaxLimit+1)
	}
}

func TestGardenReviewRestartResumesEveryRunningRun(t *testing.T) {
	d := newGardenDaemon(t)
	installGardenReviewRunner(t, d, false)
	for _, id := range []string{"r-first", "r-second"} {
		run := garden.ReviewRun{
			ID: id, CandidateIDs: []string{"s-" + id}, Status: garden.ReviewRunStatusRunning,
			CapturedAt: formatGardenTime(time.Now()),
			Recipe:     garden.ReviewRecipe{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"},
		}
		item := garden.ReviewItem{
			ID: garden.ReviewItemID(id, "s-"+id), RunID: id, SeedID: "s-" + id,
			EvidenceVersion: "e-" + id, Status: garden.ReviewItemStatusQueued,
			Resolution: garden.ReviewResolutionUnresolved, Evidence: []garden.ReviewEvidence{}, Actions: []string{},
		}
		if err := d.createGardenReview(run, []garden.ReviewItem{item}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	d.resumeGardenReviews()
	for _, id := range []string{"r-first", "r-second"} {
		itemID := garden.ReviewItemID(id, "s-"+id)
		job, err := d.jobQueue.GetByKey(gardenReviewClassifyKind, itemID)
		if err != nil || job == nil {
			t.Fatalf("resumed job %s = %+v err=%v", itemID, job, err)
		}
	}
}

func oldUnheldGrowingSeed(t *testing.T, d *Daemon, now time.Time) protocol.Seed {
	t.Helper()
	old := now.Add(-garden.DefaultStaleWindow)
	d.gardenNow = func() time.Time { return old }
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Old growing work", Body: protocol.Ptr("Finish and verify the work.")})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	d.store.Remove("sess-a")
	d.gardenNow = func() time.Time { return now }
	return seed
}

func installGardenReviewRunner(t *testing.T, d *Daemon, start bool) {
	t.Helper()
	runner := jobs.New(jobs.Options{
		Store: newTestJobStore(t, d), Log: func(string, ...interface{}) {},
		PollInterval: time.Millisecond,
	})
	if err := runner.RegisterWith(gardenReviewClassifyKind, d.gardenReviewClassifyHandler,
		jobs.HandlerConfig{Timeout: gardenReviewClassifyTimeout}); err != nil {
		t.Fatalf("register Garden review handler: %v", err)
	}
	if start {
		if err := runner.Start(); err != nil {
			t.Fatalf("start Garden review runner: %v", err)
		}
		t.Cleanup(runner.Stop)
	}
	d.jobQueue = runner
}

func readyGardenReviewItem(
	t *testing.T,
	d *Daemon,
	run garden.ReviewRun,
	item garden.ReviewItem,
) garden.ReviewItem {
	t.Helper()
	item.Status = garden.ReviewItemStatusReady
	item.Recommendation = "harvest"
	item.Explanation = "The captured work looks complete."
	item.CitedEvidence = []string{"The seed body states the completed outcome."}
	item.CompletedAt = formatGardenTime(d.gardenTime())
	if err := d.finishGardenReviewItem(gardenReviewJobPayload{
		RunID: run.ID, ItemID: item.ID, EvidenceVersion: item.EvidenceVersion,
	}, item); err != nil {
		t.Fatalf("finishGardenReviewItem: %v", err)
	}
	return item
}
