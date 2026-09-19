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
	Author    string
	Content   string
	CreatedAt time.Time
}

type Comment struct {
	ID        string
	Author    string
	Body      string
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
	HeadSHA        string
	HeadObservedAt time.Time
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
	UnavailableCause string
}

var reviewedCommitPattern = regexp.MustCompile(`(?i)reviewed\s+commit\s*[:*` + "`" + `\s]*([0-9a-f]{7,40})`)

func Evaluate(evidence Evidence, reviewer string) Evaluation {
	result := Evaluation{ReviewState: ReviewWaiting}
	reviewer = strings.TrimSuffix(strings.TrimSpace(reviewer), "[bot]")

	for _, thread := range evidence.Threads {
		if thread.Resolved || !sameActor(thread.Author, reviewer) {
			continue
		}
		result.Unresolved = append(result.Unresolved, Finding{
			ID: thread.ID, Author: thread.Author, Body: thread.Body,
			Location: thread.Location, Resolved: thread.Resolved,
		})
	}

	var current *Review
	for i := range evidence.Reviews {
		review := &evidence.Reviews[i]
		if !sameActor(review.Author, reviewer) || !reviewMatchesHead(*review, evidence.HeadSHA) {
			continue
		}
		if current == nil || review.SubmittedAt.After(current.SubmittedAt) {
			current = review
		}
	}

	var latestSignal time.Time
	if current != nil {
		result.ReviewBody = strings.TrimSpace(current.Body)
		result.ReviewSubmitted = current.SubmittedAt
		latestSignal = current.SubmittedAt
		if unavailableReason(current.Body) != "" {
			result.ReviewState = ReviewUnavailable
			result.UnavailableCause = unavailableReason(current.Body)
		} else {
			switch strings.ToUpper(strings.TrimSpace(current.State)) {
			case "CHANGES_REQUESTED":
				result.ReviewState = ReviewChangesRequested
			case "APPROVED":
				result.ReviewState = ReviewApproved
			case "COMMENTED":
				if !isCodexReviewer(reviewer) {
					break
				}
				if len(current.Findings) == 0 {
					result.ReviewState = ReviewApproved
				} else {
					result.ReviewState = ReviewChangesRequested
					result.Findings = append(result.Findings, current.Findings...)
				}
			}
		}
	}

	if isCodexReviewer(reviewer) {
		for _, reaction := range evidence.Reactions {
			if !sameActor(reaction.Author, reviewer) || !strings.EqualFold(reaction.Content, "THUMBS_UP") {
				continue
			}
			if evidence.HeadObservedAt.IsZero() || reaction.CreatedAt.Before(evidence.HeadObservedAt) ||
				!reaction.CreatedAt.After(latestSignal) {
				continue
			}
			result.ReviewState = ReviewApproved
			result.ReviewSubmitted = reaction.CreatedAt
			result.ReviewBody = ""
			result.Findings = nil
			result.UnavailableCause = ""
			latestSignal = reaction.CreatedAt
		}
	}

	for _, comment := range evidence.Comments {
		if !sameActor(comment.Author, reviewer) || evidence.HeadObservedAt.IsZero() ||
			comment.CreatedAt.Before(evidence.HeadObservedAt) {
			continue
		}
		if reason := unavailableReason(comment.Body); reason != "" {
			if !comment.CreatedAt.After(latestSignal) {
				continue
			}
			result.ReviewState = ReviewUnavailable
			result.UnavailableCause = reason
			result.ReviewSubmitted = comment.CreatedAt
			result.ReviewBody = strings.TrimSpace(comment.Body)
			result.Findings = nil
			latestSignal = comment.CreatedAt
		}
	}

	if len(result.Unresolved) > 0 && result.ReviewState == ReviewApproved {
		result.ReviewState = ReviewChangesRequested
	}
	result.Ready = strings.EqualFold(evidence.State, "open") && !evidence.Draft &&
		evidence.CheckState == ChecksGreen && result.ReviewState == ReviewApproved &&
		len(result.Unresolved) == 0
	return result
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
		"review unavailable", "review limit", "usage limit", "rate limit", "quota",
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
