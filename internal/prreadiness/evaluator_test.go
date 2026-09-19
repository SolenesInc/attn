package prreadiness

import (
	"testing"
	"time"
)

func TestEvaluateCodexSignalsAreExactHead(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "clean", HeadSHA: "abcdef1234567890", HeadObservedAt: headAt,
		CheckState: ChecksGreen,
		Reviews: []Review{{
			ID: "review", Author: "chatgpt-codex-connector", State: "COMMENTED",
			CommitOID: "abcdef1234567890", Body: "Reviewed commit: `abcdef1`",
			SubmittedAt: headAt.Add(time.Minute),
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); !got.Ready || got.ReviewState != ReviewApproved {
		t.Fatalf("clean current-head review = %+v", got)
	}

	evidence.Reviews[0].Body = "Reviewed commit: `0000000`"
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); got.Ready || got.ReviewState != ReviewWaiting {
		t.Fatalf("stale receipt = %+v", got)
	}
}

func TestEvaluateCodexFindingsAndThreadsBlock(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "clean", HeadSHA: "abcdef1234567890", HeadObservedAt: headAt,
		CheckState: ChecksGreen,
		Reviews: []Review{{
			Author: "chatgpt-codex-connector", State: "COMMENTED", CommitOID: "abcdef1234567890",
			SubmittedAt: headAt.Add(time.Minute),
			Findings:    []Finding{{ID: "finding", Body: "the guard is missing", Location: "watch.go:42"}},
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); got.Ready || got.ReviewState != ReviewChangesRequested || len(got.Findings) != 1 {
		t.Fatalf("COMMENTED finding = %+v", got)
	}

	evidence.Reviews[0].State = "APPROVED"
	evidence.Reviews[0].Findings = nil
	evidence.Threads = []Thread{{ID: "thread", Author: "chatgpt-codex-connector", Body: "still open"}}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); got.Ready || len(got.Unresolved) != 1 {
		t.Fatalf("unresolved thread = %+v", got)
	}
}

func TestEvaluateCodexReactionNeedsCurrentCycle(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{State: "open", MergeableState: "clean", HeadSHA: "abcdef", HeadObservedAt: headAt, CheckState: ChecksGreen}
	evidence.Reactions = []Reaction{
		{Author: "chatgpt-codex-connector", Content: "EYES", CreatedAt: headAt.Add(time.Minute)},
		{Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: headAt.Add(-time.Minute)},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); got.Ready {
		t.Fatalf("eyes or stale thumbs-up passed: %+v", got)
	}
	evidence.Reactions = append(evidence.Reactions, Reaction{
		Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: headAt.Add(time.Minute),
	})
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); !got.Ready {
		t.Fatalf("current-cycle thumbs-up did not pass: %+v", got)
	}
}

func TestEvaluateCodexReactionCannotCrossHeadObservation(t *testing.T) {
	observedAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "clean", HeadSHA: "new-head", HeadObservedAt: observedAt, CheckState: ChecksGreen,
		Reactions: []Reaction{{
			Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: observedAt.Add(-time.Second),
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); got.Ready {
		t.Fatalf("reaction from the previous observed head passed: %+v", got)
	}
	evidence.Reactions = append(evidence.Reactions, Reaction{
		Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: observedAt.Add(time.Second),
	})
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); !got.Ready {
		t.Fatalf("reaction after the head observation did not pass: %+v", got)
	}
}

func TestEvaluateLaterReactionSupersedesFindingAndUnavailable(t *testing.T) {
	observedAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "clean", HeadSHA: "head", HeadObservedAt: observedAt, CheckState: ChecksGreen,
		Reviews: []Review{{
			Author: "chatgpt-codex-connector", State: "COMMENTED", CommitOID: "head",
			SubmittedAt: observedAt.Add(time.Minute), Findings: []Finding{{ID: "old", Body: "old finding"}},
		}},
		Comments: []Comment{{
			Author: "chatgpt-codex-connector", CreatedAt: observedAt.Add(2 * time.Minute), Body: "Review unavailable because quota is exhausted.",
		}},
		Reactions: []Reaction{{
			Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: observedAt.Add(3 * time.Minute),
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); !got.Ready || len(got.Findings) != 0 || got.UnavailableCause != "" {
		t.Fatalf("later clean reaction did not supersede earlier signals: %+v", got)
	}
}

func TestEvaluateCodexUnavailableAndHelpText(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{State: "open", MergeableState: "clean", HeadSHA: "abcdef", HeadObservedAt: headAt, CheckState: ChecksGreen,
		Comments: []Comment{{Author: "chatgpt-codex-connector", CreatedAt: headAt.Add(time.Minute), Body: "Codex can review this pull request."}}}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); got.Ready || got.ReviewState != ReviewWaiting {
		t.Fatalf("help text became a pass: %+v", got)
	}
	evidence.Comments[0].Body = "Review unavailable because the quota is exhausted."
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); got.ReviewState != ReviewUnavailable {
		t.Fatalf("quota was not exposed: %+v", got)
	}
	evidence.Reviews = []Review{{
		Author: "chatgpt-codex-connector", State: "COMMENTED", CommitOID: "abcdef",
		SubmittedAt: headAt.Add(2 * time.Minute),
	}}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); !got.Ready {
		t.Fatalf("a later successful review did not supersede unavailability: %+v", got)
	}
}

func TestEvaluateRequiresMergeableHead(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "dirty", HeadSHA: "abcdef", HeadObservedAt: headAt,
		CheckState: ChecksGreen,
		Reviews: []Review{{
			Author: "chatgpt-codex-connector", State: "APPROVED", CommitOID: "abcdef",
			SubmittedAt: headAt.Add(time.Minute),
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); got.Ready {
		t.Fatalf("dirty head became ready: %+v", got)
	}
	evidence.MergeableState = "clean"
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]"); !got.Ready {
		t.Fatalf("clean head did not become ready: %+v", got)
	}
}

func TestEvaluateReviewVocabularyDoesNotLookLikeOutage(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "clean", HeadSHA: "abcdef", HeadObservedAt: headAt,
		CheckState: ChecksGreen,
		Reviews: []Review{{
			Author: "chatgpt-codex-connector", State: "COMMENTED", CommitOID: "abcdef",
			Body: "The rate limit and quota handling need tests.", SubmittedAt: headAt.Add(time.Minute),
			Findings: []Finding{{ID: "finding", Body: "Add coverage for the rate limit."}},
		}},
	}
	got := Evaluate(evidence, "chatgpt-codex-connector[bot]")
	if got.ReviewState != ReviewChangesRequested || len(got.Findings) != 1 || got.UnavailableCause != "" {
		t.Fatalf("review vocabulary became an outage: %+v", got)
	}
}
