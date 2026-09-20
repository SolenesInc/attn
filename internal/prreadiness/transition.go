package prreadiness

import (
	"sort"
	"strings"
	"time"
)

type Cursor struct {
	Initialized       bool     `json:"initialized,omitempty"`
	Reviewer          string   `json:"reviewer,omitempty"`
	HeadSHA           string   `json:"head_sha,omitempty"`
	SignalBaselineIDs []string `json:"signal_baseline_ids,omitempty"`
	SeenCommentIDs    []string `json:"seen_comment_ids,omitempty"`
	SeenVerdictIDs    []string `json:"seen_verdict_ids,omitempty"`
	LastActionKey     string   `json:"last_action_key,omitempty"`
}

type StartPolicy struct {
	Since                            time.Time
	HoldExistingVerdictWhenRequested bool
	IgnoreAuthors                    []string
	EmitReviewerVerdictFeedback      bool
}

type Outcome string

const (
	OutcomeClosed            Outcome = "closed"
	OutcomeChecksFailed      Outcome = "checks_failed"
	OutcomeChangesRequested  Outcome = "changes_requested"
	OutcomeReviewUnavailable Outcome = "review_unavailable"
	OutcomeHumanComment      Outcome = "comment"
	OutcomeReady             Outcome = "approved"
	OutcomeBotComment        Outcome = "bot_comment"
)

type EventKind string

const (
	EventAction      EventKind = "action"
	EventClearAction EventKind = "clear_action"
	EventFeedback    EventKind = "feedback"
)

type Event struct {
	ID       string
	Kind     EventKind
	Outcomes []Outcome
	Details  []string
	Comments []Comment
}

type Transition struct {
	Evaluation      Evaluation
	Events          []Event
	BaselineCursor  Cursor
	NextCursor      Cursor
	HeadChanged     bool
	ReviewerChanged bool
}

func Advance(previous Cursor, observation Observation, reviewer string, policy StartPolicy) Transition {
	reviewer = NormalizeActor(reviewer)
	first := !previous.Initialized
	reviewerChanged := previous.Initialized && !SameActor(previous.Reviewer, reviewer)
	headChanged := previous.Initialized && previous.HeadSHA != observation.HeadSHA

	baseline := cloneCursor(previous)
	if first {
		baseline = Cursor{SeenCommentIDs: baselineCommentIDs(observation.Comments, policy.Since)}
	} else if reviewerChanged {
		baseline = Cursor{SeenCommentIDs: append([]string(nil), previous.SeenCommentIDs...)}
	}
	baseline.Initialized = true
	baseline.Reviewer = reviewer
	if first || reviewerChanged || headChanged {
		cutoff := time.Time{}
		if first {
			cutoff = policy.Since
		}
		baseline.HeadSHA = observation.HeadSHA
		baseline.SignalBaselineIDs = UnscopedSignalIDs(observation, reviewer, cutoff)
		baseline.LastActionKey = ""
	}
	if (first || reviewerChanged) && policy.HoldExistingVerdictWhenRequested && reviewerRequested(observation, reviewer) {
		baseline.SeenVerdictIDs = VerdictSignalIDs(observation, reviewer, time.Time{})
	}
	baseline.SeenCommentIDs = uniqueSorted(baseline.SeenCommentIDs)
	baseline.SeenVerdictIDs = uniqueSorted(baseline.SeenVerdictIDs)

	evaluation := Evaluate(observation, reviewer, baseline.SignalBaselineIDs)
	next := cloneCursor(baseline)
	events := feedbackEvents(observation, reviewer, baseline.SeenCommentIDs, policy)
	for _, event := range events {
		for _, comment := range event.Comments {
			next.SeenCommentIDs = append(next.SeenCommentIDs, comment.ID)
		}
	}

	outcomes, details := currentAction(observation, evaluation, reviewer, baseline, policy)
	actionKey := ""
	if len(outcomes) > 0 {
		parts := make([]string, 0, len(outcomes))
		for _, outcome := range outcomes {
			parts = append(parts, string(outcome))
		}
		actionKey = Fingerprint(strings.Join(parts, "+"), observation.HeadSHA, details)
	}
	if actionKey != baseline.LastActionKey {
		if actionKey == "" {
			events = append(events, Event{ID: "clear:" + baseline.LastActionKey, Kind: EventClearAction})
		} else {
			events = append(events, Event{
				ID: "action:" + actionKey, Kind: EventAction,
				Outcomes: outcomes, Details: details,
			})
		}
		next.LastActionKey = actionKey
	}
	if evaluation.SignalID != "" {
		next.SeenVerdictIDs = append(next.SeenVerdictIDs, evaluation.SignalID)
	}
	next.SeenCommentIDs = uniqueSorted(next.SeenCommentIDs)
	next.SeenVerdictIDs = uniqueSorted(next.SeenVerdictIDs)

	return Transition{
		Evaluation: evaluation, Events: events, BaselineCursor: baseline, NextCursor: next,
		HeadChanged: headChanged, ReviewerChanged: reviewerChanged,
	}
}

func cloneCursor(cursor Cursor) Cursor {
	cursor.SignalBaselineIDs = append([]string(nil), cursor.SignalBaselineIDs...)
	cursor.SeenCommentIDs = append([]string(nil), cursor.SeenCommentIDs...)
	cursor.SeenVerdictIDs = append([]string(nil), cursor.SeenVerdictIDs...)
	return cursor
}

func baselineCommentIDs(comments []Comment, since time.Time) []string {
	ids := make([]string, 0, len(comments))
	for _, comment := range comments {
		if comment.ID != "" && (since.IsZero() || !comment.CreatedAt.After(since)) {
			ids = append(ids, comment.ID)
		}
	}
	return uniqueSorted(ids)
}

func feedbackEvents(observation Observation, reviewer string, seenIDs []string, policy StartPolicy) []Event {
	seen := stringSet(seenIDs)
	var events []Event
	for _, comment := range observation.Comments {
		formalReviewerVerdict := comment.Kind == CommentReview && SameActor(comment.Author, reviewer) &&
			isFormalReviewState(comment.ReviewState)
		if comment.ID == "" || seen[comment.ID] || ignoredAuthor(comment.Author, policy.IgnoreAuthors) ||
			(!policy.Since.IsZero() && !comment.CreatedAt.After(policy.Since)) ||
			(formalReviewerVerdict && !policy.EmitReviewerVerdictFeedback) {
			continue
		}
		outcome := OutcomeHumanComment
		if comment.Bot {
			outcome = OutcomeBotComment
		}
		events = append(events, Event{
			ID: "comment:" + comment.ID, Kind: EventFeedback,
			Outcomes: []Outcome{outcome}, Comments: []Comment{comment},
		})
	}
	return events
}

func currentAction(
	observation Observation,
	evaluation Evaluation,
	reviewer string,
	cursor Cursor,
	policy StartPolicy,
) ([]Outcome, []string) {
	var outcomes []Outcome
	var details []string
	if !strings.EqualFold(observation.State, "open") {
		state := strings.ToLower(observation.State)
		if observation.Merged {
			state = "merged"
		}
		return []Outcome{OutcomeClosed}, []string{"pull request is " + state}
	}
	if observation.CheckState == ChecksFailed {
		outcomes = append(outcomes, OutcomeChecksFailed)
		details = append(details, observation.FailedChecks()...)
	}

	verdictFresh := evaluation.SignalID != "" && !stringSet(cursor.SeenVerdictIDs)[evaluation.SignalID]
	holdVerdict := policy.HoldExistingVerdictWhenRequested && reviewerRequested(observation, reviewer) && !verdictFresh
	if evaluation.ReviewState == ReviewUnavailable && !holdVerdict {
		outcomes = append(outcomes, OutcomeReviewUnavailable)
		details = append(details, evaluation.UnavailableCause)
	}
	findings := uniqueFindings(evaluation.Findings, evaluation.Unresolved)
	if len(evaluation.Unresolved) > 0 || evaluation.ReviewState == ReviewChangesRequested && !holdVerdict {
		outcomes = append(outcomes, OutcomeChangesRequested)
		for _, finding := range findings {
			line := strings.TrimSpace(finding.Body)
			if finding.Location != "" {
				line = finding.Location + ": " + line
			}
			if line != "" {
				details = append(details, line)
			}
		}
		if len(findings) == 0 && evaluation.ReviewBody != "" {
			details = append(details, evaluation.ReviewBody)
		}
	}
	if evaluation.Ready && !holdVerdict {
		outcomes = append(outcomes, OutcomeReady)
		details = append(details, "checks passed", reviewer+" reviewed the current head")
	}
	sort.Strings(details)
	return outcomes, details
}

func reviewerRequested(observation Observation, reviewer string) bool {
	for _, requested := range observation.RequestedReviewers {
		if SameActor(requested, reviewer) {
			return true
		}
	}
	return false
}

func ignoredAuthor(author string, ignored []string) bool {
	for _, candidate := range ignored {
		if SameActor(author, candidate) {
			return true
		}
	}
	return false
}

func isFormalReviewState(state string) bool {
	state = strings.ToUpper(strings.TrimSpace(state))
	return state == "APPROVED" || state == "CHANGES_REQUESTED"
}

func uniqueFindings(groups ...[]Finding) []Finding {
	var findings []Finding
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, finding := range group {
			key := strings.TrimSpace(finding.ID)
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(finding.Location)) + "\x00" +
					strings.ToLower(strings.Join(strings.Fields(finding.Body), " "))
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			findings = append(findings, finding)
		}
	}
	return findings
}

func stringSet(ids []string) map[string]bool {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	return seen
}
