package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/toolhome"
)

func TestGardenReviewOffersResumeOnlyWithUsableContinuation(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-garden.DefaultStaleWindow)
	d.gardenNow = func() time.Time { return old }
	cwd := t.TempDir()
	toolHome := t.TempDir()
	t.Setenv(toolhome.EnvVar, toolHome)
	conversation := filepath.Join(toolHome, ".copilot", "session-state", "native-1")
	if err := os.MkdirAll(conversation, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conversation, "events.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d.store.Remove("sess-a")
	d.store.Add(&protocol.Session{
		ID: "sess-a", Directory: cwd, Agent: protocol.SessionAgentCopilot, State: protocol.SessionStateIdle,
	})
	d.store.SetResumeSessionID("sess-a", "native-1")
	d.store.SetLaunchIntent("sess-a", store.LaunchIntent{})
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Resumable old work"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	d.closeSession("sess-a", store.SessionClose{By: store.SessionClosedByUser})
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

func TestGardenReviewKeptSeedReturnsOnceTheStaleWindowPasses(t *testing.T) {
	d := newGardenDaemon(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	seed := oldUnheldGrowingSeed(t, d, now)
	installGardenReviewRunner(t, d, false)
	run, items, err := d.startGardenReview()
	if err != nil {
		t.Fatalf("startGardenReview: %v", err)
	}
	review := protocol.SeedReviewActionContext{ReviewID: run.ID, EvidenceVersion: items[0].EvidenceVersion}
	if _, _, err := d.keepGardenReviewItem(review, seed.ID); err != nil {
		t.Fatalf("keepGardenReviewItem: %v", err)
	}

	d.gardenNow = func() time.Time { return now.Add(garden.DefaultStaleWindow - time.Second) }
	if _, _, count, err := d.gardenReviewOverview(); err != nil || count != 0 {
		t.Fatalf("candidates before the review window = %d err=%v", count, err)
	}
	d.gardenNow = func() time.Time { return now.Add(garden.DefaultStaleWindow) }
	if _, _, count, err := d.gardenReviewOverview(); err != nil || count != 1 {
		t.Fatalf("candidates at the review window = %d err=%v", count, err)
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
