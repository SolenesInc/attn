package prreadiness

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ChecksNone    = "none"
	ChecksPending = "pending"
	ChecksGreen   = "green"
	ChecksFailed  = "failed"

	ReviewWaiting          = "waiting"
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewUnavailable      = "unavailable"
	ReviewUnresolved       = "unresolved_threads"
)

type Finding struct {
	ID       string
	Author   string
	Body     string
	Location string
	Resolved bool
}

type Review struct {
	ID          string
	Author      string
	State       string
	Body        string
	CommitOID   string
	SubmittedAt time.Time
	Findings    []Finding
}

type Reaction struct {
	ID        string
	Author    string
	Content   string
	CreatedAt time.Time
}

const (
	CommentIssue  = "issue"
	CommentReview = "review"
	CommentInline = "inline"
)

type Comment struct {
	ID        string
	Author    string
	Kind      string
	Body      string
	Location  string
	CreatedAt time.Time
	Bot       bool
}

type Thread struct {
	ID        string
	Author    string
	CommitOID string
	Resolved  bool
	Body      string
	Location  string
}

type Evidence struct {
	State          string
	Draft          bool
	MergeableState string
	HeadSHA        string
	CheckState     string
	FailedChecks   []string
	Reviews        []Review
	Reactions      []Reaction
	Comments       []Comment
	Threads        []Thread
}

type Evaluation struct {
	ReviewState      string
	Ready            bool
	Findings         []Finding
	Unresolved       []Finding
	ReviewBody       string
	ReviewSubmitted  time.Time
	SignalID         string
	UnavailableCause string
}

var reviewedCommitPattern = regexp.MustCompile(`(?i)reviewed\s+commit\s*[:*` + "`" + `\s]*([0-9a-f]{7,40})`)

type verdictSignal struct {
	ID       string
	State    string
	Body     string
	Cause    string
	At       time.Time
	Findings []Finding
	priority int
}

func Evaluate(evidence Evidence, reviewer string, baselineIDs []string) Evaluation {
	reviewer = strings.TrimSuffix(strings.TrimSpace(reviewer), "[bot]")
	baseline := make(map[string]bool, len(baselineIDs))
	for _, id := range baselineIDs {
		baseline[id] = true
	}
	result := Evaluation{ReviewState: ReviewWaiting}
	for _, thread := range evidence.Threads {
		if !thread.Resolved {
			result.Unresolved = append(result.Unresolved, Finding{
				ID: thread.ID, Author: thread.Author, Body: thread.Body,
				Location: thread.Location, Resolved: thread.Resolved,
			})
		}
	}

	signal, found := latestReviewSignal(evidence, reviewer)
	if isCodexReviewer(reviewer) {
		for _, reaction := range evidence.Reactions {
			candidate := verdictSignal{
				ID: reaction.ID, State: ReviewApproved, At: reaction.CreatedAt, priority: 2,
			}
			if candidate.ID != "" && !baseline[candidate.ID] && sameActor(reaction.Author, reviewer) &&
				strings.EqualFold(reaction.Content, "THUMBS_UP") && candidate.laterThan(signal, found) {
				signal, found = candidate, true
			}
		}
	}
	for _, comment := range evidence.Comments {
		reason := unavailableReason(comment.Body)
		candidate := verdictSignal{
			ID: comment.ID, State: ReviewUnavailable, Body: strings.TrimSpace(comment.Body),
			Cause: reason, At: comment.CreatedAt, priority: 1,
		}
		if candidate.ID != "" && reason != "" && !baseline[candidate.ID] &&
			eligibleOutageComment(comment, reviewer) && candidate.laterThan(signal, found) {
			signal, found = candidate, true
		}
	}
	if found {
		result.ReviewState = signal.State
		result.ReviewBody = signal.Body
		result.ReviewSubmitted = signal.At
		result.SignalID = signal.ID
		result.Findings = append(result.Findings, signal.Findings...)
		result.UnavailableCause = signal.Cause
	}

	if len(result.Unresolved) > 0 && result.ReviewState == ReviewApproved {
		result.ReviewState = ReviewUnresolved
	}
	result.Ready = strings.EqualFold(evidence.State, "open") && !evidence.Draft &&
		strings.EqualFold(evidence.MergeableState, "clean") &&
		evidence.CheckState == ChecksGreen && result.ReviewState == ReviewApproved &&
		len(result.Unresolved) == 0
	return result
}

func latestReviewSignal(evidence Evidence, reviewer string) (verdictSignal, bool) {
	var latest Review
	found := false
	for _, review := range evidence.Reviews {
		if !sameActor(review.Author, reviewer) || !reviewMatchesHead(review, evidence.HeadSHA) ||
			!isCodexReviewer(reviewer) && strings.EqualFold(review.State, "COMMENTED") {
			continue
		}
		if !found || review.SubmittedAt.After(latest.SubmittedAt) ||
			review.SubmittedAt.Equal(latest.SubmittedAt) && review.ID > latest.ID {
			latest, found = review, true
		}
	}
	if !found {
		return verdictSignal{}, false
	}
	return signalForReview(latest), true
}

func signalForReview(review Review) verdictSignal {
	signal := verdictSignal{
		ID: review.ID, State: ReviewWaiting, Body: strings.TrimSpace(review.Body),
		At: review.SubmittedAt, priority: 3,
	}
	switch strings.ToUpper(strings.TrimSpace(review.State)) {
	case "CHANGES_REQUESTED":
		signal.State = ReviewChangesRequested
		signal.Findings = review.Findings
	case "APPROVED":
		signal.State = ReviewApproved
	case "COMMENTED":
		if len(review.Findings) == 0 {
			signal.State = ReviewApproved
		} else {
			signal.State, signal.Findings = ReviewChangesRequested, review.Findings
		}
	}
	return signal
}

func (signal verdictSignal) laterThan(current verdictSignal, found bool) bool {
	if !found || signal.At.After(current.At) {
		return true
	}
	if !signal.At.Equal(current.At) {
		return false
	}
	if signal.priority != current.priority {
		return signal.priority > current.priority
	}
	return signal.ID > current.ID
}

func UnscopedSignalIDs(evidence Evidence, reviewer string) []string {
	var ids []string
	for _, reaction := range evidence.Reactions {
		if reaction.ID != "" && sameActor(reaction.Author, reviewer) && strings.EqualFold(reaction.Content, "THUMBS_UP") {
			ids = append(ids, reaction.ID)
		}
	}
	for _, comment := range evidence.Comments {
		if comment.ID != "" && eligibleOutageComment(comment, reviewer) && unavailableReason(comment.Body) != "" {
			ids = append(ids, comment.ID)
		}
	}
	return uniqueSorted(ids)
}

func VerdictSignalIDs(evidence Evidence, reviewer string) []string {
	var ids []string
	for _, review := range evidence.Reviews {
		if review.ID == "" || !sameActor(review.Author, reviewer) || !reviewMatchesHead(review, evidence.HeadSHA) {
			continue
		}
		state := strings.ToUpper(strings.TrimSpace(review.State))
		if state == "APPROVED" || state == "CHANGES_REQUESTED" || state == "COMMENTED" && isCodexReviewer(reviewer) {
			ids = append(ids, review.ID)
		}
	}
	return uniqueSorted(ids)
}

func eligibleOutageComment(comment Comment, reviewer string) bool {
	return isCodexReviewer(reviewer) && comment.Bot && comment.Kind == CommentIssue && sameActor(comment.Author, reviewer)
}

func uniqueSorted(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	unique := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	sort.Strings(unique)
	return unique
}

func isCodexReviewer(reviewer string) bool {
	return strings.EqualFold(strings.TrimSuffix(reviewer, "[bot]"), "chatgpt-codex-connector")
}

func reviewMatchesHead(review Review, head string) bool {
	head = strings.ToLower(strings.TrimSpace(head))
	if head == "" {
		return false
	}
	commit := strings.ToLower(strings.TrimSpace(review.CommitOID))
	if commit != "" && commit != head {
		return false
	}
	match := reviewedCommitPattern.FindStringSubmatch(review.Body)
	if len(match) == 2 && !strings.HasPrefix(head, strings.ToLower(match[1])) {
		return false
	}
	return commit == head || len(match) == 2
}

func sameActor(left, right string) bool {
	left = strings.TrimSuffix(strings.TrimSpace(left), "[bot]")
	right = strings.TrimSuffix(strings.TrimSpace(right), "[bot]")
	return strings.EqualFold(left, right)
}

func unavailableReason(body string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(body), " "))
	for _, phrase := range []string{
		"unable to review", "cannot review", "can't review", "could not review",
		"review unavailable", "review limit reached", "usage limit reached",
		"rate limit reached", "rate limit exceeded", "quota exhausted", "quota exceeded",
	} {
		if strings.Contains(normalized, phrase) {
			return phrase
		}
	}
	return ""
}

func Fingerprint(kind, head string, details []string) string {
	stable := append([]string(nil), details...)
	sort.Strings(stable)
	sum := sha256.Sum256([]byte(kind + "\x00" + head + "\x00" + strings.Join(stable, "\x00")))
	return hex.EncodeToString(sum[:12])
}
