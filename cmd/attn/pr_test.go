package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/prreadiness"
)

type fakePRSource struct {
	observation  *prreadiness.Observation
	feedback     []prreadiness.FeedbackItem
	threads      []prreadiness.ThreadState
	readinessErr error
	feedbackErr  error
}

func (source fakePRSource) Readiness(context.Context, prWaitOptions) (*prreadiness.Observation, error) {
	if source.readinessErr != nil {
		return nil, source.readinessErr
	}
	copy := *source.observation
	return &copy, nil
}

func (source fakePRSource) Feedback(context.Context, prWaitOptions) ([]prreadiness.FeedbackItem, []prreadiness.ThreadState, error) {
	return source.feedback, source.threads, source.feedbackErr
}

func readyPR() *prreadiness.Observation {
	return &prreadiness.Observation{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "open", HeadSHA: "abcdef012345", MergeStateStatus: "CLEAN"}
}

func waitOptions() prWaitOptions {
	return prWaitOptions{Host: "github.com", Owner: "acme", Name: "widgets", Number: 7, Mode: prreadiness.ModeGreen, Timeout: time.Minute, Interval: time.Second}
}

func TestParsePRWaitArgsModesAndHosts(t *testing.T) {
	tests := []struct {
		args       []string
		wantMode   prreadiness.Mode
		wantHost   string
		wantReview string
	}{
		{args: []string{"7", "--repo", "acme/widgets"}, wantMode: prreadiness.ModeGreen, wantHost: "github.com"},
		{args: []string{"https://ghe.example/acme/widgets/pull/7", "--mode", "codex"}, wantMode: prreadiness.ModeCodex, wantHost: "ghe.example"},
		{args: []string{"7", "--repo", "ghe.example/acme/widgets", "--mode", "formal-review", "--reviewer", "victor"}, wantMode: prreadiness.ModeFormalReview, wantHost: "ghe.example", wantReview: "victor"},
	}
	for _, test := range tests {
		got, err := parsePRWaitArgs(test.args)
		if err != nil {
			t.Fatalf("parse %v: %v", test.args, err)
		}
		if got.Mode != test.wantMode || got.Host != test.wantHost || got.Reviewer != test.wantReview {
			t.Fatalf("parse %v = %+v", test.args, got)
		}
	}
	if _, err := parsePRWaitArgs([]string{"7", "--repo", "acme/widgets", "--reviewer", "victor"}); err == nil {
		t.Fatal("green accepted --reviewer")
	}
}

func TestObservePRReturnsCurrentReadinessAndBaselinesFeedback(t *testing.T) {
	now := time.Now()
	source := fakePRSource{observation: readyPR(), feedback: []prreadiness.FeedbackItem{{ID: "old", Kind: "comment", Author: "a", CreatedAt: now.Add(-time.Hour)}}}
	result := observePR(context.Background(), source, waitOptions(), prWaitCursor{}, now)
	if result.Outcome != outcomeReady || len(result.Actions) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Cursor.Readiness.SeenFeedbackIDs) != 1 {
		t.Fatalf("cursor = %+v", result.Cursor)
	}
	if result.BeforeOutputCursor.Readiness.LastAction != "" {
		t.Fatalf("before-output cursor acknowledged ready: %+v", result.BeforeOutputCursor)
	}
}

func TestObservePRResumesFeedbackAndReopenedThreads(t *testing.T) {
	now := time.Now()
	opts := waitOptions()
	firstSource := fakePRSource{
		observation: readyPR(),
		feedback:    []prreadiness.FeedbackItem{{ID: "old", Kind: "comment", Author: "a"}},
		threads:     []prreadiness.ThreadState{{ID: "thread", Resolved: true}},
	}
	first := observePR(context.Background(), firstSource, opts, prWaitCursor{}, now)
	secondSource := fakePRSource{
		observation: readyPR(),
		feedback: []prreadiness.FeedbackItem{
			{ID: "old", Kind: "comment", Author: "a"},
			{ID: "reply", Kind: "reply", Author: "b", Body: "answer"},
		},
		threads: []prreadiness.ThreadState{{ID: "thread", Resolved: false}},
	}
	second := observePR(context.Background(), secondSource, opts, first.Cursor, now.Add(time.Minute))
	if len(second.Outcomes) != 2 || second.Outcomes[0] != outcomeReply || second.Outcomes[1] != outcomeThreadReopened {
		t.Fatalf("outcomes = %+v actions=%+v", second.Outcomes, second.Actions)
	}
}

func TestObservePROutagesCoalesceAndRecoverSilently(t *testing.T) {
	now := time.Now()
	opts := waitOptions()
	failing := fakePRSource{readinessErr: errors.New("rate limited")}
	first := observePR(context.Background(), failing, opts, prWaitCursor{}, now)
	if first.Outcome != outcomeMonitoringOutage || !first.Cursor.OutageActive {
		t.Fatalf("first = %+v", first)
	}
	second := observePR(context.Background(), failing, opts, first.Cursor, now.Add(time.Minute))
	if second.Outcome != "" {
		t.Fatalf("repeated outage = %+v", second)
	}
	recovered := observePR(context.Background(), fakePRSource{observation: readyPR()}, opts, second.Cursor, now.Add(2*time.Minute))
	if recovered.Cursor.OutageActive || recovered.Outcome != outcomeReady {
		t.Fatalf("recovery = %+v", recovered)
	}
}

func TestFeedbackFailureLeavesReadinessUsableAndVisible(t *testing.T) {
	result := observePR(context.Background(), fakePRSource{observation: readyPR(), feedbackErr: errors.New("threads unavailable")}, waitOptions(), prWaitCursor{}, time.Now())
	if result.Outcome != outcomeReady || result.Health != "delayed" || !strings.Contains(result.HealthError, "threads unavailable") {
		t.Fatalf("result = %+v", result)
	}
}

func TestFeedbackRecoveryPreservesSinceCutoff(t *testing.T) {
	now := time.Now()
	opts := waitOptions()
	opts.Since = now.Add(-time.Minute)
	observation := readyPR()
	observation.MergeStateStatus = "BLOCKED"
	first := observePR(context.Background(), fakePRSource{
		observation: observation,
		feedbackErr: errors.New("threads unavailable"),
	}, opts, prWaitCursor{}, now)
	if !first.Cursor.Readiness.FeedbackBaselinePending {
		t.Fatalf("first cursor = %+v", first.Cursor)
	}
	second := observePR(context.Background(), fakePRSource{
		observation: observation,
		feedback: []prreadiness.FeedbackItem{
			{ID: "old", Kind: "comment", CreatedAt: now.Add(-time.Hour)},
			{ID: "new", Kind: "review", CreatedAt: now},
		},
	}, opts, first.Cursor, now.Add(time.Second))
	if second.Outcome != outcomeComment || len(second.Actions) != 1 || second.Actions[0].ID != "feedback:new" {
		t.Fatalf("recovery result = %+v", second)
	}
}

func TestSinceReportsOnlyNewFeedback(t *testing.T) {
	now := time.Now()
	opts := waitOptions()
	opts.Since = now.Add(-time.Minute)
	observation := readyPR()
	observation.MergeStateStatus = "BLOCKED"
	result := observePR(context.Background(), fakePRSource{
		observation: observation,
		feedback: []prreadiness.FeedbackItem{
			{ID: "old", Kind: "comment", Author: "a", CreatedAt: now.Add(-time.Hour)},
			{ID: "new", Kind: "comment", Author: "b", CreatedAt: now},
		},
	}, opts, prWaitCursor{}, now)
	if result.Outcome != outcomeComment || len(result.Actions) != 1 || result.Actions[0].Feedback.ID != "new" {
		t.Fatalf("result = %+v", result)
	}
}

func TestReportPROutcomeIncludesModeStateHealthAndDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute).UTC()
	result := prWaitResult{
		Outcome: outcomeReady, Outcomes: []prOutcome{outcomeReady}, Observation: readyPR(),
		Evaluation: prreadiness.Evaluation{State: "ready", Reason: "green", Description: "ready", SettlingUntil: &deadline},
		Health:     "ok",
	}
	opts := waitOptions()
	opts.JSON = true
	var output bytes.Buffer
	if code := reportPROutcome(result, opts, &output); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{`"mode": "green"`, `"state": "ready"`, `"health": "ok"`, `"head": "abcdef012345"`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output %q missing %q", output.String(), want)
		}
	}
}

func TestCursorRoundTripPreservesModeTemporalAndDeliveryState(t *testing.T) {
	dir := t.TempDir()
	opts := waitOptions()
	now := time.Now().UTC().Truncate(time.Second)
	cursor := prWaitCursor{
		Mode: prreadiness.ModeCodex, OutageActive: true,
		Readiness: prreadiness.Cursor{Initialized: true, HeadSHA: "head", HeadObservedAt: now, SeenFeedbackIDs: []string{"f"}, ThreadStates: map[string]bool{"t": true}},
	}
	if err := savePRWaitCursor(dir, opts, cursor, now); err != nil {
		t.Fatal(err)
	}
	got, err := loadPRWaitCursor(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != cursor.Mode || !got.OutageActive || got.Readiness.HeadSHA != "head" || !got.Readiness.ThreadStates["t"] {
		t.Fatalf("cursor = %+v", got)
	}
}
