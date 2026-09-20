package prreadiness

import (
	"slices"
	"testing"
	"time"
)

func TestEvaluateCodexSignalsAreExactHead(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "clean", HeadSHA: "abcdef1234567890",
		CheckState: ChecksGreen,
		Reviews: []Review{{
			ID: "review", Author: "chatgpt-codex-connector", State: "COMMENTED",
			CommitOID: "abcdef1234567890", Body: "Reviewed commit: `abcdef1`",
			SubmittedAt: headAt.Add(time.Minute),
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); !got.Ready || got.ReviewState != ReviewApproved {
		t.Fatalf("clean current-head review = %+v", got)
	}

	evidence.Reviews[0].Body = "Reviewed commit: `0000000`"
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); got.Ready || got.ReviewState != ReviewWaiting {
		t.Fatalf("stale receipt = %+v", got)
	}
}

func TestEvaluateCodexFindingsAndThreadsBlock(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "clean", HeadSHA: "abcdef1234567890",
		CheckState: ChecksGreen,
		Reviews: []Review{{
			Author: "chatgpt-codex-connector", State: "COMMENTED", CommitOID: "abcdef1234567890",
			SubmittedAt: headAt.Add(time.Minute),
			Findings:    []Finding{{ID: "finding", Body: "the guard is missing", Location: "watch.go:42"}},
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); got.Ready || got.ReviewState != ReviewChangesRequested || len(got.Findings) != 1 {
		t.Fatalf("COMMENTED finding = %+v", got)
	}

	evidence.Reviews[0].State = "APPROVED"
	evidence.Reviews[0].Findings = nil
	evidence.Threads = []Thread{{ID: "thread", Author: "chatgpt-codex-connector", Body: "still open"}}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); got.Ready || len(got.Unresolved) != 1 {
		t.Fatalf("unresolved thread = %+v", got)
	}
}

func TestEvaluateCodexReactionNeedsCurrentCycle(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{State: "open", MergeableState: "clean", HeadSHA: "abcdef", CheckState: ChecksGreen}
	evidence.Reactions = []Reaction{
		{ID: "eyes", Author: "chatgpt-codex-connector", Content: "EYES", CreatedAt: headAt.Add(time.Minute)},
		{ID: "old", Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: headAt.Add(time.Minute)},
	}
	baseline := UnscopedSignalIDs(evidence, "chatgpt-codex-connector[bot]")
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", baseline); got.Ready {
		t.Fatalf("baselined thumbs-up passed: %+v", got)
	}
	evidence.Reactions = append(evidence.Reactions, Reaction{
		ID: "new", Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: headAt.Add(-time.Hour),
	})
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", baseline); !got.Ready || got.SignalID != "new" {
		t.Fatalf("new reaction identity did not pass despite clock skew: %+v", got)
	}
	evidence.Reactions = evidence.Reactions[:2]
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", baseline); got.Ready || got.ReviewState != ReviewWaiting {
		t.Fatalf("removed approval remained ready: %+v", got)
	}
}

func TestEvaluateEqualTimeSignalsAreOrderIndependent(t *testing.T) {
	at := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	reviews := []Review{
		{ID: "a", Author: "chatgpt-codex-connector", State: "CHANGES_REQUESTED", CommitOID: "head", SubmittedAt: at},
		{ID: "b", Author: "chatgpt-codex-connector", State: "APPROVED", CommitOID: "head", SubmittedAt: at},
	}
	for range 2 {
		evidence := Evidence{
			State: "open", MergeableState: "clean", HeadSHA: "head", CheckState: ChecksGreen,
			Reviews: reviews,
			Reactions: []Reaction{{
				ID: "reaction", Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: at,
			}},
			Comments: []Comment{{
				ID: "outage", Author: "chatgpt-codex-connector", Kind: CommentIssue, Bot: true,
				Body: "Review unavailable because quota is exhausted.", CreatedAt: at,
			}},
		}
		if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); !got.Ready || got.SignalID != "b" {
			t.Fatalf("equal-time order selected a different verdict: %+v", got)
		}
		slices.Reverse(reviews)
	}
}

func TestEvaluateLaterReactionSupersedesFindingAndUnavailable(t *testing.T) {
	observedAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "clean", HeadSHA: "head", CheckState: ChecksGreen,
		Reviews: []Review{{
			ID: "review", Author: "chatgpt-codex-connector", State: "COMMENTED", CommitOID: "head",
			SubmittedAt: observedAt.Add(time.Minute), Findings: []Finding{{ID: "old", Body: "old finding"}},
		}},
		Comments: []Comment{{
			ID: "outage", Author: "chatgpt-codex-connector", Kind: CommentIssue, Bot: true,
			CreatedAt: observedAt.Add(2 * time.Minute), Body: "Review unavailable because quota is exhausted.",
		}},
		Reactions: []Reaction{{
			ID: "reaction", Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: observedAt.Add(3 * time.Minute),
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); !got.Ready || len(got.Findings) != 0 || got.UnavailableCause != "" {
		t.Fatalf("later clean reaction did not supersede earlier signals: %+v", got)
	}
}

func TestEvaluateCodexUnavailableAndHelpText(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{State: "open", MergeableState: "clean", HeadSHA: "abcdef", CheckState: ChecksGreen,
		Comments: []Comment{{ID: "status", Author: "chatgpt-codex-connector", Kind: CommentIssue, Bot: true,
			CreatedAt: headAt.Add(time.Minute), Body: "Codex can review this pull request."}}}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); got.Ready || got.ReviewState != ReviewWaiting {
		t.Fatalf("help text became a pass: %+v", got)
	}
	evidence.Comments[0].Body = "Review unavailable because the quota is exhausted."
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); got.ReviewState != ReviewUnavailable {
		t.Fatalf("quota was not exposed: %+v", got)
	}
	evidence.Reviews = []Review{{
		ID: "review", Author: "chatgpt-codex-connector", State: "COMMENTED", CommitOID: "abcdef",
		SubmittedAt: headAt.Add(2 * time.Minute),
	}}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); !got.Ready {
		t.Fatalf("a later successful review did not supersede unavailability: %+v", got)
	}
}

func TestEvaluateRequiresMergeableHead(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	evidence := Evidence{
		State: "open", MergeableState: "dirty", HeadSHA: "abcdef",
		CheckState: ChecksGreen,
		Reviews: []Review{{
			Author: "chatgpt-codex-connector", State: "APPROVED", CommitOID: "abcdef",
			SubmittedAt: headAt.Add(time.Minute),
		}},
	}
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); got.Ready {
		t.Fatalf("dirty head became ready: %+v", got)
	}
	evidence.MergeableState = "clean"
	if got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil); !got.Ready {
		t.Fatalf("clean head did not become ready: %+v", got)
	}
}

func TestEvaluateFormalVerdictCannotBecomeOutage(t *testing.T) {
	headAt := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, state, want string
		findings          []Finding
	}{
		{name: "approved", state: "APPROVED", want: ReviewApproved},
		{name: "changes requested", state: "CHANGES_REQUESTED", want: ReviewChangesRequested,
			findings: []Finding{{ID: "finding", Body: "Keep the rate limit guard."}}},
		{name: "clean Codex comment", state: "COMMENTED", want: ReviewApproved},
		{name: "Codex comment with findings", state: "COMMENTED", want: ReviewChangesRequested,
			findings: []Finding{{ID: "finding", Body: "Keep the rate limit guard."}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := Evidence{
				State: "open", MergeableState: "clean", HeadSHA: "abcdef", CheckState: ChecksGreen,
				Reviews: []Review{{
					ID: "review", Author: "chatgpt-codex-connector", State: test.state, CommitOID: "abcdef",
					Body: "Quota exceeded handling looks good.", SubmittedAt: headAt.Add(time.Minute),
					Findings: test.findings,
				}},
				Comments: []Comment{
					{ID: "review", Author: "chatgpt-codex-connector", Kind: CommentReview, Bot: true,
						Body: "Quota exceeded handling looks good.", CreatedAt: headAt.Add(time.Minute)},
					{ID: "finding", Author: "chatgpt-codex-connector", Kind: CommentInline, Bot: true,
						Body: "Keep the rate limit guard.", CreatedAt: headAt.Add(time.Minute)},
				},
			}
			got := Evaluate(evidence, "chatgpt-codex-connector[bot]", nil)
			if got.ReviewState != test.want || got.UnavailableCause != "" || got.SignalID != "review" {
				t.Fatalf("formal verdict became outage: %+v", got)
			}
		})
	}
}
