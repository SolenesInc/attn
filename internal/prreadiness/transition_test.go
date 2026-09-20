package prreadiness

import (
	"slices"
	"testing"
	"time"
)

func TestReadinessTransitionMatrix(t *testing.T) {
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	ready := func(head, reviewID string) Observation {
		return Observation{
			Number: 303, State: "open", HeadSHA: head, MergeableState: "clean", CheckState: ChecksGreen,
			Checks: []Check{{Name: "CI", State: ChecksGreen}},
			Reviews: []Review{{
				ID: reviewID, Author: "reviewer", State: "APPROVED", CommitOID: head, SubmittedAt: base,
			}},
		}
	}
	eventOutcomes := func(events []Event) []Outcome {
		var outcomes []Outcome
		for _, event := range events {
			outcomes = append(outcomes, event.Outcomes...)
		}
		return outcomes
	}
	has := func(events []Event, outcome Outcome) bool {
		return slices.Contains(eventOutcomes(events), outcome)
	}

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "first fetch baselines comments but reports unresolved threads",
			run: func(t *testing.T) {
				observation := ready("head-a", "approval-a")
				observation.Comments = []Comment{{ID: "existing", Author: "human", CreatedAt: base}}
				observation.Threads = []Thread{{ID: "old-thread", Body: "resolve me", CommitOID: "old-head"}}
				got := Advance(Cursor{}, observation, "reviewer", StartPolicy{})
				if got.Evaluation.ReviewState != ReviewUnresolved || !has(got.Events, OutcomeChangesRequested) || has(got.Events, OutcomeHumanComment) ||
					!slices.Contains(got.BaselineCursor.SeenCommentIDs, "existing") {
					t.Fatalf("transition = %+v", got)
				}
			},
		},
		{
			name: "explicit since replays only later feedback",
			run: func(t *testing.T) {
				observation := ready("head-a", "approval-a")
				observation.Comments = []Comment{
					{ID: "old", Author: "human", CreatedAt: base.Add(-time.Minute)},
					{ID: "new", Author: "human", CreatedAt: base.Add(time.Minute)},
				}
				got := Advance(Cursor{}, observation, "reviewer", StartPolicy{Since: base})
				if !has(got.Events, OutcomeHumanComment) || !slices.Contains(got.BaselineCursor.SeenCommentIDs, "old") ||
					slices.Contains(got.BaselineCursor.SeenCommentIDs, "new") {
					t.Fatalf("transition = %+v", got)
				}
			},
		},
		{
			name: "explicit since replays later verdict during pending rereview",
			run: func(t *testing.T) {
				observation := ready("head-a", "approval-a")
				observation.Reviews[0].SubmittedAt = base.Add(time.Minute)
				observation.RequestedReviewers = []string{"reviewer"}
				got := Advance(Cursor{}, observation, "reviewer", StartPolicy{
					Since: base, HoldExistingVerdictWhenRequested: true,
				})
				if !has(got.Events, OutcomeReady) || slices.Contains(got.BaselineCursor.SeenVerdictIDs, "approval-a") {
					t.Fatalf("transition = %+v", got)
				}
			},
		},
		{
			name: "pending rereview holds existing verdict",
			run: func(t *testing.T) {
				observation := ready("head-a", "approval-a")
				observation.RequestedReviewers = []string{"reviewer"}
				first := Advance(Cursor{}, observation, "reviewer", StartPolicy{HoldExistingVerdictWhenRequested: true})
				if has(first.Events, OutcomeReady) {
					t.Fatalf("existing verdict emitted: %+v", first)
				}
				observation.Reviews = append(observation.Reviews, Review{
					ID: "approval-b", Author: "reviewer", State: "APPROVED", CommitOID: "head-a", SubmittedAt: base.Add(time.Minute),
				})
				second := Advance(first.NextCursor, observation, "reviewer", StartPolicy{HoldExistingVerdictWhenRequested: true})
				if !has(second.Events, OutcomeReady) {
					t.Fatalf("fresh verdict not emitted: %+v", second)
				}
			},
		},
		{
			name: "reviewer change holds the new reviewer's existing verdict",
			run: func(t *testing.T) {
				observation := ready("head-a", "approval-a")
				first := Advance(Cursor{}, observation, "reviewer", StartPolicy{HoldExistingVerdictWhenRequested: true})
				observation.RequestedReviewers = []string{"other-reviewer"}
				observation.Reviews = append(observation.Reviews, Review{
					ID: "other-approval", Author: "other-reviewer", State: "APPROVED", CommitOID: "head-a", SubmittedAt: base,
				})
				changed := Advance(first.NextCursor, observation, "other-reviewer", StartPolicy{HoldExistingVerdictWhenRequested: true})
				if !changed.ReviewerChanged || has(changed.Events, OutcomeReady) ||
					!slices.Contains(changed.BaselineCursor.SeenVerdictIDs, "other-approval") {
					t.Fatalf("reviewer transition = %+v", changed)
				}
			},
		},
		{
			name: "formal findings and approval transition",
			run: func(t *testing.T) {
				observation := ready("head-a", "review-a")
				observation.Reviews[0].State = "CHANGES_REQUESTED"
				observation.Reviews[0].Findings = []Finding{{ID: "finding", Body: "fix it"}}
				first := Advance(Cursor{}, observation, "reviewer", StartPolicy{})
				if !has(first.Events, OutcomeChangesRequested) {
					t.Fatalf("findings = %+v", first)
				}
				observation.Reviews = append(observation.Reviews, Review{
					ID: "review-b", Author: "reviewer", State: "APPROVED", CommitOID: "head-a", SubmittedAt: base.Add(time.Minute),
				})
				second := Advance(first.NextCursor, observation, "reviewer", StartPolicy{})
				if !has(second.Events, OutcomeReady) || !second.Evaluation.Ready {
					t.Fatalf("approval = %+v", second)
				}
			},
		},
		{
			name: "formal reviewer body is optional durable feedback",
			run: func(t *testing.T) {
				initial := Observation{
					State: "open", HeadSHA: "head-a", MergeableState: "clean", CheckState: ChecksGreen,
				}
				first := Advance(Cursor{}, initial, "reviewer", StartPolicy{EmitReviewerVerdictFeedback: true})
				observation := ready("head-a", "approval-a")
				observation.Reviews[0].Body = "Ship it, with this rollout caveat."
				observation.Reviews[0].SubmittedAt = base.Add(time.Minute)
				observation.Comments = []Comment{{
					ID: "approval-a", Author: "reviewer", Kind: CommentReview, ReviewState: "APPROVED",
					Body: "Ship it, with this rollout caveat.", CreatedAt: base.Add(time.Minute),
				}}
				daemon := Advance(first.NextCursor, observation, "reviewer", StartPolicy{EmitReviewerVerdictFeedback: true})
				cli := Advance(first.NextCursor, observation, "reviewer", StartPolicy{})
				if !has(daemon.Events, OutcomeReady) || !has(daemon.Events, OutcomeHumanComment) ||
					has(cli.Events, OutcomeHumanComment) {
					t.Fatalf("daemon = %+v, cli = %+v", daemon, cli)
				}
			},
		},
		{
			name: "reaction freshness uses identity despite clock skew",
			run: func(t *testing.T) {
				observation := Observation{
					State: "open", HeadSHA: "head-a", MergeableState: "clean", CheckState: ChecksGreen,
					Reactions: []Reaction{{ID: "old", Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: base}},
				}
				first := Advance(Cursor{}, observation, "chatgpt-codex-connector[bot]", StartPolicy{})
				observation.Reactions = append(observation.Reactions, Reaction{
					ID: "new", Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: base.Add(-time.Hour),
				})
				second := Advance(first.NextCursor, observation, "chatgpt-codex-connector[bot]", StartPolicy{})
				if !has(second.Events, OutcomeReady) || second.Evaluation.SignalID != "new" {
					t.Fatalf("skewed reaction = %+v", second)
				}
			},
		},
		{
			name: "new head baselines unscoped signals and keeps old threads blocking",
			run: func(t *testing.T) {
				first := Advance(Cursor{}, ready("head-a", "review-a"), "reviewer", StartPolicy{})
				observation := Observation{
					State: "open", HeadSHA: "head-b", MergeableState: "clean", CheckState: ChecksGreen,
					Reactions: []Reaction{{ID: "old-reaction", Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: base}},
					Threads: []Thread{
						{ID: "old", CommitOID: "head-a", Body: "old unresolved"},
						{ID: "current", CommitOID: "head-b", Body: "current unresolved"},
					},
				}
				second := Advance(first.NextCursor, observation, "chatgpt-codex-connector", StartPolicy{})
				if !second.HeadChanged || !has(second.Events, OutcomeChangesRequested) || len(second.Evaluation.Unresolved) != 2 ||
					second.Evaluation.ReviewState == ReviewApproved {
					t.Fatalf("head transition = %+v", second)
				}
			},
		},
		{
			name: "partial delivery retries stable event then next cursor suppresses it",
			run: func(t *testing.T) {
				observation := ready("head-a", "review-a")
				first := Advance(Cursor{}, observation, "reviewer", StartPolicy{})
				observation.Comments = []Comment{{ID: "human", Author: "human", Body: "look", CreatedAt: base.Add(time.Minute)}}
				attempt := Advance(first.NextCursor, observation, "reviewer", StartPolicy{})
				retry := Advance(attempt.BaselineCursor, observation, "reviewer", StartPolicy{})
				if !has(attempt.Events, OutcomeHumanComment) || len(retry.Events) != len(attempt.Events) || retry.Events[0].ID != attempt.Events[0].ID {
					t.Fatalf("retry = %+v after %+v", retry, attempt)
				}
				delivered := Advance(attempt.NextCursor, observation, "reviewer", StartPolicy{})
				if has(delivered.Events, OutcomeHumanComment) {
					t.Fatalf("delivered feedback replayed: %+v", delivered)
				}
			},
		},
		{
			name: "recovery clears action and refailure emits again",
			run: func(t *testing.T) {
				failed := Observation{
					State: "open", HeadSHA: "head-a", MergeableState: "clean", CheckState: ChecksFailed,
					Checks: []Check{{Name: "CI", State: ChecksFailed}},
				}
				first := Advance(Cursor{}, failed, "reviewer", StartPolicy{})
				recovered := failed
				recovered.CheckState = ChecksPending
				recovered.Checks[0].State = ChecksPending
				second := Advance(first.NextCursor, recovered, "reviewer", StartPolicy{})
				third := Advance(second.NextCursor, failed, "reviewer", StartPolicy{})
				if !has(first.Events, OutcomeChecksFailed) || len(second.Events) != 1 || second.Events[0].Kind != EventClearAction ||
					!has(third.Events, OutcomeChecksFailed) || first.Events[0].ID == third.Events[0].ID ||
					third.NextCursor.ActionGeneration != 1 {
					t.Fatalf("failure lifecycle = first:%+v recovered:%+v refailed:%+v", first, second, third)
				}
			},
		},
		{
			name: "closure emits and reviewer spelling change does not rearm",
			run: func(t *testing.T) {
				observation := ready("head-a", "review-a")
				first := Advance(Cursor{}, observation, "reviewer[BOT]", StartPolicy{})
				same := Advance(first.NextCursor, observation, "reviewer", StartPolicy{})
				if same.ReviewerChanged {
					t.Fatalf("bot suffix changed generation: %+v", same)
				}
				observation.State = "closed"
				closed := Advance(same.NextCursor, observation, "other-reviewer", StartPolicy{})
				if !closed.ReviewerChanged || !has(closed.Events, OutcomeClosed) {
					t.Fatalf("closure/reviewer transition = %+v", closed)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}

func TestEvaluateFormalVerdictCannotBecomeOutage(t *testing.T) {
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	observation := Observation{
		State: "open", HeadSHA: "head", MergeableState: "clean", CheckState: ChecksGreen,
		Reviews: []Review{{
			ID: "review", Author: "reviewer", State: "APPROVED", CommitOID: "head",
			Body: "Quota exceeded handling looks good.", SubmittedAt: at,
		}},
		Comments: []Comment{{
			ID: "review", Author: "reviewer", Kind: CommentReview, ReviewState: "APPROVED",
			Body: "Quota exceeded handling looks good.", CreatedAt: at,
		}},
	}
	got := Evaluate(observation, "reviewer", nil)
	if !got.Ready || got.ReviewState != ReviewApproved || got.UnavailableCause != "" {
		t.Fatalf("formal verdict became outage: %+v", got)
	}
}
