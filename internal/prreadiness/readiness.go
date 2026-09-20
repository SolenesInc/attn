package prreadiness

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type Mode string

const (
	ModeGreen        Mode = "green"
	ModeCodex        Mode = "codex"
	ModeFormalReview Mode = "formal-review"

	CodexActor  = "chatgpt-codex-connector[bot]"
	CodexSettle = 30 * time.Second
)

func ParseMode(value string) (Mode, error) {
	mode := Mode(strings.TrimSpace(strings.ToLower(value)))
	if mode == "" {
		return ModeGreen, nil
	}
	switch mode {
	case ModeGreen, ModeCodex, ModeFormalReview:
		return mode, nil
	default:
		return "", fmt.Errorf("mode %q is invalid; use green, codex, or formal-review", value)
	}
}

func ValidateConfig(mode Mode, reviewer string) error {
	if mode != ModeGreen && mode != ModeCodex && mode != ModeFormalReview {
		return fmt.Errorf("mode %q is invalid; use green, codex, or formal-review", mode)
	}
	if strings.TrimSpace(reviewer) != "" && mode != ModeFormalReview {
		return fmt.Errorf("--reviewer is only valid with --mode formal-review")
	}
	return nil
}

type Check struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
}

type FeedbackItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Author    string    `json:"author"`
	Body      string    `json:"body,omitempty"`
	Location  string    `json:"location,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type ThreadState struct {
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
	Author   string `json:"author,omitempty"`
	Body     string `json:"body,omitempty"`
	Location string `json:"location,omitempty"`
}

type ReviewOpinion struct {
	Actor string `json:"actor,omitempty"`
	State string `json:"state,omitempty"`
	Body  string `json:"body,omitempty"`
}

type Observation struct {
	Number           int             `json:"number"`
	URL              string          `json:"url"`
	Title            string          `json:"title"`
	State            string          `json:"state"`
	Draft            bool            `json:"draft"`
	Merged           bool            `json:"merged"`
	HeadSHA          string          `json:"head_sha"`
	HeadRef          string          `json:"head_ref"`
	MergeStateStatus string          `json:"merge_state_status"`
	ReviewDecision   string          `json:"review_decision,omitempty"`
	CodexThumbsUp    bool            `json:"codex_thumbs_up"`
	CodexEyes        bool            `json:"codex_eyes"`
	ReviewOpinions   []ReviewOpinion `json:"review_opinions,omitempty"`
	Checks           []Check         `json:"checks,omitempty"`
	Feedback         []FeedbackItem  `json:"feedback,omitempty"`
	Threads          []ThreadState   `json:"threads,omitempty"`
	FeedbackComplete bool            `json:"feedback_complete"`
}

type Cursor struct {
	Initialized             bool              `json:"initialized"`
	FeedbackBaselinePending bool              `json:"feedback_baseline_pending,omitempty"`
	HeadSHA                 string            `json:"head_sha,omitempty"`
	HeadObservedAt          time.Time         `json:"head_observed_at,omitempty"`
	SeenFeedbackIDs         []string          `json:"seen_feedback_ids,omitempty"`
	ThreadStates            map[string]bool   `json:"thread_states,omitempty"`
	ThreadGenerations       map[string]uint64 `json:"thread_generations,omitempty"`
	LastAction              string            `json:"last_action,omitempty"`
	ActionGeneration        uint64            `json:"action_generation,omitempty"`
	LastReviewDecision      string            `json:"last_review_decision,omitempty"`
}

type Evaluation struct {
	State         string     `json:"state"`
	Reason        string     `json:"reason"`
	Description   string     `json:"description"`
	SettlingUntil *time.Time `json:"settling_until,omitempty"`
}

const (
	StateReady   = "ready"
	StateWaiting = "waiting"
	StateBlocked = "blocked"
	StateMerged  = "merged"
	StateClosed  = "closed"
	StateUnknown = "unknown"
)

func Evaluate(observation Observation, mode Mode, reviewer string, cursor Cursor, now time.Time) (Evaluation, Cursor) {
	now = now.UTC()
	if observation.HeadSHA != "" && observation.HeadSHA != cursor.HeadSHA {
		cursor.HeadSHA = observation.HeadSHA
		cursor.HeadObservedAt = now
		if cursor.LastAction != "" {
			cursor.ActionGeneration++
		}
		cursor.LastAction = ""
	}

	state := strings.ToLower(strings.TrimSpace(observation.State))
	if observation.Merged || state == StateMerged {
		return Evaluation{State: StateMerged, Reason: "pull_request_merged", Description: "pull request merged"}, cursor
	}
	if state == StateClosed {
		return Evaluation{State: StateClosed, Reason: "pull_request_closed", Description: "pull request closed"}, cursor
	}
	if state != "open" {
		return Evaluation{State: StateUnknown, Reason: "pull_request_state_unknown", Description: "GitHub returned an unknown pull request state"}, cursor
	}
	if observation.Draft {
		return Evaluation{State: StateBlocked, Reason: "draft", Description: "pull request is a draft"}, cursor
	}
	if observation.HeadSHA == "" {
		return Evaluation{State: StateUnknown, Reason: "head_unknown", Description: "GitHub returned no pull request head"}, cursor
	}

	mergeState := strings.ToUpper(strings.TrimSpace(observation.MergeStateStatus))
	if mergeState != "CLEAN" && mergeState != "HAS_HOOKS" {
		reason, description := mergeStateReason(mergeState)
		return Evaluation{State: stateForMergeState(mergeState), Reason: reason, Description: description}, cursor
	}

	switch mode {
	case ModeGreen:
		return Evaluation{State: StateReady, Reason: "green", Description: "GitHub reports the pull request ready to merge or enqueue"}, cursor
	case ModeCodex:
		if cursor.HeadObservedAt.IsZero() {
			cursor.HeadObservedAt = now
		}
		deadline := cursor.HeadObservedAt.Add(CodexSettle)
		if now.Before(deadline) {
			return Evaluation{State: StateWaiting, Reason: "codex_settling", Description: "waiting for the Codex review signal to settle", SettlingUntil: &deadline}, cursor
		}
		if observation.CodexEyes {
			return Evaluation{State: StateWaiting, Reason: "codex_reviewing", Description: "Codex is reviewing the pull request"}, cursor
		}
		if observation.CodexThumbsUp {
			return Evaluation{State: StateReady, Reason: "codex_approved_best_effort", Description: "Codex has a current thumbs-up reaction (best effort)"}, cursor
		}
		return Evaluation{State: StateWaiting, Reason: "codex_approval_missing", Description: "waiting for a Codex thumbs-up reaction"}, cursor
	case ModeFormalReview:
		if strings.TrimSpace(reviewer) == "" {
			switch strings.ToUpper(strings.TrimSpace(observation.ReviewDecision)) {
			case "APPROVED":
				return Evaluation{State: StateReady, Reason: "formal_review_approved", Description: "GitHub reports the pull request approved"}, cursor
			case "CHANGES_REQUESTED":
				return Evaluation{State: StateWaiting, Reason: "changes_requested", Description: "a formal review requested changes"}, cursor
			case "REVIEW_REQUIRED":
				return Evaluation{State: StateWaiting, Reason: "review_required", Description: "GitHub reports that a review is required"}, cursor
			case "":
				return Evaluation{State: StateWaiting, Reason: "formal_approval_missing", Description: "waiting for a formal approval"}, cursor
			default:
				return Evaluation{State: StateUnknown, Reason: "review_decision_unknown", Description: "GitHub returned an unknown review decision"}, cursor
			}
		}
		opinion := reviewOpinion(observation.ReviewOpinions, reviewer)
		switch strings.ToUpper(strings.TrimSpace(opinion.State)) {
		case "APPROVED":
			return Evaluation{State: StateReady, Reason: "selected_reviewer_approved", Description: reviewer + " approved the pull request"}, cursor
		case "CHANGES_REQUESTED":
			return Evaluation{State: StateWaiting, Reason: "changes_requested", Description: reviewer + " requested changes"}, cursor
		case "DISMISSED":
			return Evaluation{State: StateWaiting, Reason: "review_dismissed", Description: reviewer + "'s review was dismissed"}, cursor
		case "":
			return Evaluation{State: StateWaiting, Reason: "selected_reviewer_approval_missing", Description: "waiting for " + reviewer + " to approve"}, cursor
		default:
			return Evaluation{State: StateUnknown, Reason: "selected_reviewer_opinion_unknown", Description: "GitHub returned an unknown selected-reviewer opinion"}, cursor
		}
	default:
		return Evaluation{State: StateUnknown, Reason: "mode_unknown", Description: "readiness mode is unknown"}, cursor
	}
}

func mergeStateReason(state string) (string, string) {
	switch state {
	case "BLOCKED":
		return "repository_policy_blocked", "GitHub reports the pull request blocked by repository policy"
	case "BEHIND":
		return "behind_base", "the pull request branch is behind its base"
	case "DIRTY":
		return "merge_conflict", "the pull request has merge conflicts"
	case "UNSTABLE":
		return "checks_not_green", "one or more checks are not green"
	case "DRAFT":
		return "draft", "pull request is a draft"
	case "", "UNKNOWN":
		return "merge_state_unknown", "GitHub has not determined the pull request merge state"
	default:
		return "merge_state_unknown", "GitHub returned an unknown pull request merge state"
	}
}

func stateForMergeState(state string) string {
	if state == "" || state == "UNKNOWN" {
		return StateUnknown
	}
	return StateBlocked
}

type Action struct {
	Kind     string        `json:"kind"`
	ID       string        `json:"id"`
	Details  []string      `json:"details,omitempty"`
	Feedback *FeedbackItem `json:"feedback,omitempty"`
}

type Transition struct {
	Evaluation Evaluation `json:"evaluation"`
	Cursor     Cursor     `json:"cursor"`
	Actions    []Action   `json:"actions"`
}

func Advance(cursor Cursor, observation Observation, mode Mode, reviewer string, now time.Time) Transition {
	evaluation, next := Evaluate(observation, mode, reviewer, cursor, now)
	next.ThreadStates = cloneBoolMap(next.ThreadStates)
	next.ThreadGenerations = cloneUintMap(next.ThreadGenerations)
	seen := make(map[string]bool, len(cursor.SeenFeedbackIDs))
	for _, id := range cursor.SeenFeedbackIDs {
		seen[id] = true
	}
	if next.ThreadStates == nil {
		next.ThreadStates = make(map[string]bool)
	}
	var actions []Action
	feedbackBaselinePending := !cursor.Initialized || cursor.FeedbackBaselinePending
	if !feedbackBaselinePending && observation.FeedbackComplete {
		for _, feedback := range observation.Feedback {
			if feedback.ID == "" || seen[feedback.ID] {
				continue
			}
			item := feedback
			actions = append(actions, Action{Kind: feedback.Kind, ID: "feedback:" + feedback.ID, Feedback: &item})
		}
		for _, thread := range observation.Threads {
			if wasResolved, known := cursor.ThreadStates[thread.ID]; known && wasResolved && !thread.Resolved {
				next.ThreadGenerations[thread.ID]++
				actions = append(actions, Action{
					Kind: "thread_reopened", ID: fmt.Sprintf("thread:%s:%d", thread.ID, next.ThreadGenerations[thread.ID]),
					Details: compactDetails(thread.Author, thread.Location, thread.Body),
				})
			}
		}
	}
	if observation.FeedbackComplete {
		for _, feedback := range observation.Feedback {
			if feedback.ID != "" && !seen[feedback.ID] {
				next.SeenFeedbackIDs = append(next.SeenFeedbackIDs, feedback.ID)
				seen[feedback.ID] = true
			}
		}
		sort.Strings(next.SeenFeedbackIDs)
		for _, thread := range observation.Threads {
			if thread.ID != "" {
				next.ThreadStates[thread.ID] = thread.Resolved
			}
		}
		next.FeedbackBaselinePending = false
	} else if !cursor.Initialized {
		next.FeedbackBaselinePending = true
	}

	actionKind, details := currentAction(observation, evaluation)
	fingerprint := actionKind + "\x00" + strings.Join(details, "\x00")
	if fingerprint != next.LastAction {
		if next.LastAction != "" {
			next.ActionGeneration++
		}
		if actionKind == "" {
			next.LastAction = ""
		} else {
			actions = append(actions, Action{
				Kind: actionKind, ID: fmt.Sprintf("action:%d:%s", next.ActionGeneration, fingerprint), Details: details,
			})
			next.LastAction = fingerprint
		}
	}
	next.LastReviewDecision = strings.ToUpper(strings.TrimSpace(observation.ReviewDecision))
	next.Initialized = true
	return Transition{Evaluation: evaluation, Cursor: next, Actions: actions}
}

func currentAction(observation Observation, evaluation Evaluation) (string, []string) {
	if observation.Merged || evaluation.State == StateMerged {
		return "merged", nil
	}
	if evaluation.State == StateClosed {
		return "closed", nil
	}
	failed := make([]string, 0)
	for _, check := range observation.Checks {
		if strings.EqualFold(check.State, "failure") || strings.EqualFold(check.State, "failed") {
			failed = append(failed, check.Name)
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		return "checks_failed", failed
	}
	changesRequested := strings.EqualFold(observation.ReviewDecision, "CHANGES_REQUESTED")
	changeDetails := make([]string, 0)
	for _, opinion := range observation.ReviewOpinions {
		if !strings.EqualFold(opinion.State, "CHANGES_REQUESTED") {
			continue
		}
		changesRequested = true
		if detail := strings.TrimSpace(opinion.Body); detail != "" {
			if actor := strings.TrimSpace(opinion.Actor); actor != "" {
				detail = actor + ": " + detail
			}
			changeDetails = append(changeDetails, detail)
		}
	}
	if changesRequested {
		sort.Strings(changeDetails)
		return "changes_requested", changeDetails
	}
	if evaluation.State == StateReady {
		return "ready", []string{evaluation.Reason}
	}
	return "", nil
}

func reviewOpinion(opinions []ReviewOpinion, reviewer string) ReviewOpinion {
	for _, opinion := range opinions {
		if strings.EqualFold(opinion.Actor, reviewer) {
			return opinion
		}
	}
	return ReviewOpinion{Actor: reviewer}
}

func compactDetails(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func PreserveFeedbackCursor(cursor Cursor) Cursor {
	return Cursor{
		Initialized:             cursor.Initialized,
		FeedbackBaselinePending: cursor.FeedbackBaselinePending,
		SeenFeedbackIDs:         append([]string(nil), cursor.SeenFeedbackIDs...),
		ThreadStates:            cloneThreadStates(cursor.ThreadStates),
		ThreadGenerations:       cloneUintMap(cursor.ThreadGenerations),
	}
}

func cloneThreadStates(states map[string]bool) map[string]bool {
	return cloneBoolMap(states)
}

func cloneBoolMap(states map[string]bool) map[string]bool {
	if len(states) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(states))
	for id, resolved := range states {
		cloned[id] = resolved
	}
	return cloned
}

func cloneUintMap(values map[string]uint64) map[string]uint64 {
	if len(values) == 0 {
		return make(map[string]uint64)
	}
	cloned := make(map[string]uint64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
