package prreadiness

import "time"

type CheckState string

const (
	ChecksNone    CheckState = "none"
	ChecksPending CheckState = "pending"
	ChecksGreen   CheckState = "green"
	ChecksFailed  CheckState = "failed"
)

type ReviewState string

const (
	ReviewWaiting          ReviewState = "waiting"
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes_requested"
	ReviewUnavailable      ReviewState = "unavailable"
	ReviewUnresolved       ReviewState = "unresolved_threads"
)

type Check struct {
	Name  string
	State CheckState
	URL   string
}

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
	ID          string
	Author      string
	Kind        string
	ReviewState string
	Body        string
	Location    string
	CreatedAt   time.Time
	Bot         bool
}

type Thread struct {
	ID        string
	Author    string
	CommitOID string
	Resolved  bool
	Body      string
	Location  string
}

type Observation struct {
	Number             int
	URL                string
	Title              string
	State              string
	HeadSHA            string
	HeadRef            string
	Draft              bool
	Merged             bool
	MergeableState     string
	CheckState         CheckState
	Checks             []Check
	RequestedReviewers []string
	Reviews            []Review
	Reactions          []Reaction
	Comments           []Comment
	Threads            []Thread
}

func (o Observation) FailedChecks() []string {
	failed := make([]string, 0)
	for _, check := range o.Checks {
		if check.State == ChecksFailed {
			failed = append(failed, check.Name)
		}
	}
	return failed
}
