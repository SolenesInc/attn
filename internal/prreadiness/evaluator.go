package prreadiness

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Evaluation struct {
	ReviewState      ReviewState
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
	State    ReviewState
	Body     string
	Cause    string
	At       time.Time
	Findings []Finding
	priority int
}

func Evaluate(evidence Observation, reviewer string, baselineIDs []string) Evaluation {
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
			if candidate.ID != "" && !baseline[candidate.ID] && SameActor(reaction.Author, reviewer) &&
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

	if len(result.Unresolved) > 0 &&
		(result.ReviewState == ReviewWaiting || result.ReviewState == ReviewApproved) {
		result.ReviewState = ReviewUnresolved
	}
	result.Ready = strings.EqualFold(evidence.State, "open") && !evidence.Draft &&
		strings.EqualFold(evidence.MergeableState, "clean") &&
		evidence.CheckState == ChecksGreen && result.ReviewState == ReviewApproved &&
		len(result.Unresolved) == 0
	return result
}

func latestReviewSignal(evidence Observation, reviewer string) (verdictSignal, bool) {
	var latest Review
	found := false
	for _, review := range evidence.Reviews {
		if !SameActor(review.Author, reviewer) || !reviewMatchesHead(review, evidence.HeadSHA) ||
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

func UnscopedSignalIDs(evidence Observation, reviewer string, cutoff time.Time) []string {
	var ids []string
	for _, reaction := range evidence.Reactions {
		if reaction.ID != "" && (cutoff.IsZero() || !reaction.CreatedAt.After(cutoff)) &&
			SameActor(reaction.Author, reviewer) && strings.EqualFold(reaction.Content, "THUMBS_UP") {
			ids = append(ids, reaction.ID)
		}
	}
	for _, comment := range evidence.Comments {
		if comment.ID != "" && (cutoff.IsZero() || !comment.CreatedAt.After(cutoff)) &&
			eligibleOutageComment(comment, reviewer) && unavailableReason(comment.Body) != "" {
			ids = append(ids, comment.ID)
		}
	}
	return uniqueSorted(ids)
}

func VerdictSignalIDs(evidence Observation, reviewer string, cutoff time.Time) []string {
	var ids []string
	for _, review := range evidence.Reviews {
		if review.ID == "" || (!cutoff.IsZero() && review.SubmittedAt.After(cutoff)) ||
			!SameActor(review.Author, reviewer) || !reviewMatchesHead(review, evidence.HeadSHA) {
			continue
		}
		if isFormalReviewerVerdict(review.State, reviewer) {
			ids = append(ids, review.ID)
		}
	}
	return uniqueSorted(ids)
}

func eligibleOutageComment(comment Comment, reviewer string) bool {
	return isCodexReviewer(reviewer) && comment.Bot && comment.Kind == CommentIssue && SameActor(comment.Author, reviewer)
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
	return SameActor(reviewer, "chatgpt-codex-connector")
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

func NormalizeActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if len(actor) >= len("[bot]") && strings.EqualFold(actor[len(actor)-len("[bot]"):], "[bot]") {
		actor = actor[:len(actor)-len("[bot]")]
	}
	return actor
}

func SameActor(left, right string) bool {
	return strings.EqualFold(NormalizeActor(left), NormalizeActor(right))
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
