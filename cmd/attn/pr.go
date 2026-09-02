package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/config"
)

const (
	prWaitExitApproved         = 0
	prWaitExitChecksFailed     = 1
	prWaitExitUsage            = 2
	prWaitExitChangesRequested = 3
	prWaitExitComment          = 4
	prWaitExitError            = 5
	prWaitExitBotComment       = 6
	prWaitExitTimeout          = 124

	checksNone    = "none"
	checksPending = "pending"
	checksGreen   = "green"
	checksFailed  = "failed"
)

type prOutcome string

const (
	outcomeApproved         prOutcome = "approved"
	outcomeChecksFailed     prOutcome = "checks_failed"
	outcomeChangesRequested prOutcome = "changes_requested"
	outcomeComment          prOutcome = "comment"
	outcomeBotComment       prOutcome = "bot_comment"
	outcomeClosed           prOutcome = "closed"
	outcomeTimeout          prOutcome = "timeout"
)

var prOutcomeRanking = []prOutcome{
	outcomeClosed,
	outcomeChecksFailed,
	outcomeChangesRequested,
	outcomeComment,
	outcomeApproved,
	outcomeBotComment,
}

func rankPROutcomes(events []prOutcome) (prOutcome, []prOutcome) {
	if len(events) == 0 {
		return "", nil
	}
	present := make(map[prOutcome]bool, len(events))
	for _, event := range events {
		present[event] = true
	}
	ranked := make([]prOutcome, 0, len(events))
	for _, candidate := range prOutcomeRanking {
		if present[candidate] {
			ranked = append(ranked, candidate)
		}
	}
	if len(ranked) == 0 {
		return events[0], events
	}
	return ranked[0], ranked
}

func (o prOutcome) exitCode() int {
	switch o {
	case outcomeApproved:
		return prWaitExitApproved
	case outcomeChecksFailed:
		return prWaitExitChecksFailed
	case outcomeChangesRequested:
		return prWaitExitChangesRequested
	case outcomeComment:
		return prWaitExitComment
	case outcomeBotComment:
		return prWaitExitBotComment
	case outcomeTimeout:
		return prWaitExitTimeout
	default:
		return prWaitExitError
	}
}

type prCheck struct {
	Name  string `json:"name"`
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
}

type prComment struct {
	ID        string    `json:"-"`
	Author    string    `json:"author"`
	Kind      string    `json:"kind"`
	Bot       bool      `json:"bot"`
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body,omitempty"`
	Location  string    `json:"location,omitempty"`
}

func isTrackedReviewerVerdict(author, state string, opts prWaitOptions) bool {
	if !strings.EqualFold(author, opts.Reviewer) {
		return false
	}
	return state == "APPROVED" || state == "CHANGES_REQUESTED"
}

func humanPRComments(comments []prComment) []prComment {
	return filterPRComments(comments, false)
}

func botPRComments(comments []prComment) []prComment {
	return filterPRComments(comments, true)
}

func filterPRComments(comments []prComment, bot bool) []prComment {
	result := make([]prComment, 0, len(comments))
	for _, comment := range comments {
		if comment.Bot == bot {
			result = append(result, comment)
		}
	}
	return result
}

type prReadiness struct {
	Number, State, HeadSHA, CheckState, Reviewer, ReviewState string
	Draft                                                     bool
	Checks                                                    []prCheck
	Comments                                                  []prComment
	ReviewerRequested                                         bool
	ReviewSubmittedAt                                         time.Time
	LatestReviewAt                                            time.Time
	ReviewBody                                                string
	URL                                                       string
}

func (r *prReadiness) ready() bool {
	return r.State == "open" && !r.Draft && r.CheckState == checksGreen && r.ReviewState == "approved"
}

type prReadinessSource interface {
	Fetch(context.Context, prWaitOptions) (*prReadiness, error)
}

type prWaitOptions struct {
	Host, Owner, Name string
	Number            int
	Reviewer          string
	IgnoreAuthors     []string
	Timeout, Interval time.Duration
	JSON              bool
	CursorDir         string
	Since             time.Time
	Reset             bool
	SelfLogin         string
	IncludeSelf       bool
}

func (o prWaitOptions) ignored(author string) bool {
	if o.SelfLogin != "" && strings.EqualFold(o.SelfLogin, author) {
		return true
	}
	for _, ignored := range o.IgnoreAuthors {
		if strings.EqualFold(ignored, author) {
			return true
		}
	}
	return false
}

type ghPRReadinessSource struct{}

const prSelfLoginTimeout = 15 * time.Second

var ghSelfLogin = func(ctx context.Context, host string) (string, error) {
	args := []string{"api", "user", "--jq", ".login"}
	if host != "" {
		args = append(args, "--hostname", host)
	}
	output, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func resolvePRSelfLogin(ctx context.Context, opts prWaitOptions, stderr io.Writer) string {
	if opts.IncludeSelf {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, prSelfLoginTimeout)
	defer cancel()
	login, err := ghSelfLogin(ctx, opts.Host)
	if err != nil || login == "" {
		fmt.Fprintln(stderr, "pr wait-ready: could not resolve the authenticated GitHub user; your own comments will be reported")
		return ""
	}
	return login
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }

func (s *stringSliceFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("author must not be empty")
	}
	*s = append(*s, value)
	return nil
}

func runPRCommand() {
	code := executePRCommand(os.Args[2:], os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func executePRCommand(args []string, stdout, stderr io.Writer) int {
	if (len(args) == 1 && isHelpArg(args[0])) || (len(args) == 2 && isHelpArg(args[1])) {
		writePRHelp(stdout)
		return 0
	}
	switch {
	case len(args) > 0 && (args[0] == "record" || args[0] == "forget" || args[0] == "ls"):
		return executeSessionPRCommand(args[0], args[1:], stdout, stderr)
	case len(args) == 0 || args[0] != "wait-ready":
		writePRHelp(stderr)
		return prWaitExitUsage
	}
	opts, err := parsePRWaitArgs(args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "pr wait-ready: %v\n", err)
		return prWaitExitUsage
	}
	if _, err := exec.LookPath("gh"); err != nil {
		fmt.Fprintln(stderr, "pr wait-ready: gh is required")
		return prWaitExitUsage
	}

	// Progress must never contaminate a JSON result on stdout.
	progress := stdout
	if opts.JSON {
		progress = stderr
	}

	opts.CursorDir = filepath.Join(config.DataDir(), "pr-wait")

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	opts.SelfLogin = resolvePRSelfLogin(ctx, opts, stderr)

	var cursor prWaitCursor
	if !opts.Reset {
		loaded, err := loadPRWaitCursor(opts.CursorDir, opts)
		if err != nil {
			fmt.Fprintf(stderr, "pr wait-ready: %v; starting from the current state\n", err)
		}
		cursor = loaded
	}

	result, err := waitForPRActionable(ctx, ghPRReadinessSource{}, opts, cursor, progress)
	if err != nil {
		fmt.Fprintf(stderr, "pr wait-ready: %v\n", err)
		return prWaitExitError
	}
	if err := savePRWaitCursor(opts.CursorDir, opts, result.Cursor, time.Now()); err != nil {
		fmt.Fprintf(stderr, "pr wait-ready: could not save cursor: %v\n", err)
	}
	return reportPROutcome(result, opts, stdout)
}

func reportPROutcome(wait prWaitResult, opts prWaitOptions, stdout io.Writer) int {
	result, outcome, events := wait.Observation, wait.Outcome, wait.Events
	detail := describePROutcome(result, outcome, opts)
	if opts.JSON {
		fresh := result.Comments
		if fresh == nil {
			fresh = []prComment{}
		}
		reported := make([]string, 0, len(events))
		for _, event := range events {
			reported = append(reported, string(event))
		}
		payload := map[string]any{
			"outcome": string(outcome),
			"events":  reported,
			"pr":      result.Number,
			"url":     result.URL,
			"head":    result.HeadSHA,
			"state":   result.State,
			"draft":   result.Draft,
			"detail":  detail,
			"checks": map[string]any{
				"state":  result.CheckState,
				"items":  result.Checks,
				"failed": failedChecks(result.Checks),
			},
			"review": map[string]any{
				"state":    result.ReviewState,
				"reviewer": result.Reviewer,
				"body":     result.ReviewBody,
			},
			"comments": fresh,
			"cursor":   wait.Cursor,
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return prWaitExitError
		}
		return outcome.exitCode()
	}
	fmt.Fprintf(stdout, "%s: %s\n", outcome, detail)
	for _, event := range events {
		if event == outcome {
			continue
		}
		fmt.Fprintf(stdout, "also %s: %s\n", event, describePROutcome(result, event, opts))
	}
	writePRContent(stdout, result, events)
	return outcome.exitCode()
}

func writePRContent(stdout io.Writer, result *prReadiness, events []prOutcome) {
	reported := make(map[prOutcome]bool, len(events))
	for _, event := range events {
		reported[event] = true
	}
	if failed := failedChecks(result.Checks); reported[outcomeChecksFailed] && len(failed) > 0 {
		for _, check := range failed {
			if check.URL != "" {
				fmt.Fprintf(stdout, "  %s %s\n", check.Name, check.URL)
				continue
			}
			fmt.Fprintf(stdout, "  %s\n", check.Name)
		}
	}
	if (reported[outcomeChangesRequested] || reported[outcomeApproved]) && result.ReviewBody != "" {
		fmt.Fprintf(stdout, "  --- %s ---\n%s\n", result.Reviewer, indentPRBody(result.ReviewBody))
	}
	for _, comment := range result.Comments {
		if comment.Bot && !reported[outcomeBotComment] {
			continue
		}
		if !comment.Bot && !reported[outcomeComment] {
			continue
		}
		where := comment.Author
		if comment.Location != "" {
			where += " on " + comment.Location
		}
		fmt.Fprintf(stdout, "  --- %s ---\n", where)
		if comment.Body != "" {
			fmt.Fprintln(stdout, indentPRBody(comment.Body))
		}
	}
	if result.URL != "" {
		fmt.Fprintf(stdout, "%s\n", result.URL)
	}
}

func indentPRBody(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func describePROutcome(result *prReadiness, outcome prOutcome, opts prWaitOptions) string {
	head := shortSHA(result.HeadSHA)
	switch outcome {
	case outcomeApproved:
		return fmt.Sprintf("%s approved %s; %d checks green", result.Reviewer, head, len(result.Checks))
	case outcomeChangesRequested:
		return fmt.Sprintf("%s requested changes on %s", result.Reviewer, head)
	case outcomeChecksFailed:
		return fmt.Sprintf("%s failed on %s", strings.Join(failedCheckNames(result.Checks), ", "), head)
	case outcomeComment:
		return describePRComments(humanPRComments(result.Comments))
	case outcomeBotComment:
		return describePRComments(botPRComments(result.Comments))
	case outcomeClosed:
		return fmt.Sprintf("pull request is %s", result.State)
	case outcomeTimeout:
		detail := fmt.Sprintf("no actionable update after %s (checks=%s review=%s)", opts.Timeout, result.CheckState, result.ReviewState)
		if result.ReviewerRequested && hasReviewVerdict(result) {
			detail += "; held the pre-baseline verdict, awaiting a re-review"
		}
		return detail
	default:
		return string(outcome)
	}
}

func describePRComments(comments []prComment) string {
	authors := make([]string, 0, len(comments))
	seen := map[string]bool{}
	for _, comment := range comments {
		if !seen[comment.Author] {
			seen[comment.Author] = true
			authors = append(authors, comment.Author)
		}
	}
	noun := "comments"
	if len(comments) == 1 {
		noun = "comment"
	}
	return fmt.Sprintf("%d new %s from %s", len(comments), noun, strings.Join(authors, ", "))
}

func failedChecks(checks []prCheck) []prCheck {
	failed := make([]prCheck, 0)
	for _, check := range checks {
		if check.State == checksFailed {
			failed = append(failed, check)
		}
	}
	return failed
}

func failedCheckNames(checks []prCheck) []string {
	var names []string
	for _, check := range checks {
		if check.State == checksFailed {
			names = append(names, check.Name)
		}
	}
	return names
}

func isHelpArg(arg string) bool { return arg == "-h" || arg == "--help" }

func parsePRWaitArgs(args []string) (prWaitOptions, error) {
	fs := flag.NewFlagSet("pr wait-ready", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "[host/]owner/repository")
	reviewer := fs.String("reviewer", "", "required reviewer login")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum wait")
	interval := fs.Duration("interval", 20*time.Second, "poll interval")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	reset := fs.Bool("reset", false, "forget what earlier waits reported and baseline from the current state")
	since := fs.String("since", "", "report anything after this RFC3339 instant instead of resuming")
	includeSelf := fs.Bool("include-self", false, "report your own comments as events")
	var ignore stringSliceFlag
	fs.Var(&ignore, "ignore-author", "comment author to ignore (repeatable)")

	target := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return prWaitOptions{}, err
	}
	if target == "" && fs.NArg() == 1 {
		target = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return prWaitOptions{}, errors.New("usage: attn pr wait-ready <number-or-url> --repo owner/repo --reviewer login")
	}
	if target == "" || strings.TrimSpace(*reviewer) == "" {
		return prWaitOptions{}, errors.New("target and --reviewer are required")
	}
	if *timeout <= 0 || *interval <= 0 {
		return prWaitOptions{}, errors.New("--timeout and --interval must be positive")
	}

	opts := prWaitOptions{
		Reviewer:      strings.TrimSpace(*reviewer),
		IgnoreAuthors: ignore,
		Timeout:       *timeout,
		Interval:      *interval,
		JSON:          *asJSON,
		Reset:         *reset,
		IncludeSelf:   *includeSelf,
	}
	if strings.TrimSpace(*since) != "" {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(*since))
		if err != nil {
			return prWaitOptions{}, fmt.Errorf("--since must be an RFC3339 timestamp: %w", err)
		}
		opts.Since = at
	}
	if strings.HasPrefix(target, "https://") {
		host, owner, repository, number, err := automation.ParsePullRequestURL(target)
		if err != nil {
			return prWaitOptions{}, err
		}
		opts.Host, opts.Owner, opts.Name, opts.Number = host, owner, repository, number
		return opts, nil
	}
	number, err := strconv.Atoi(target)
	if err != nil || number <= 0 {
		return prWaitOptions{}, errors.New("pull request number must be positive")
	}
	if strings.TrimSpace(*repo) == "" {
		return prWaitOptions{}, errors.New("--repo is required when the target is a number")
	}
	host, owner, name, err := parseRepoFlag(*repo)
	if err != nil {
		return prWaitOptions{}, err
	}
	opts.Host, opts.Owner, opts.Name, opts.Number = host, owner, name, number
	return opts, nil
}

func parseRepoFlag(repo string) (host, owner, name string, err error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(repo), "/"), "/")
	switch len(parts) {
	case 2:
		host, owner, name = "", parts[0], parts[1]
	case 3:
		host, owner, name = parts[0], parts[1], parts[2]
	default:
		return "", "", "", errors.New("--repo must be [host/]owner/repository")
	}
	if owner == "" || name == "" {
		return "", "", "", errors.New("--repo must be [host/]owner/repository")
	}
	return host, owner, name, nil
}

const prSnapshotQuery = `
query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      number state isDraft headRefOid url
      commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100){
        pageInfo{hasNextPage}
        nodes{__typename ... on CheckRun{name status conclusion detailsUrl} ... on StatusContext{context state targetUrl}}
      }}}}}
      reviewRequests(first:100){nodes{requestedReviewer{__typename ... on User{login}}}}
      reviews(last:100){nodes{id state bodyText submittedAt author{__typename login} commit{oid}
        comments(first:100){pageInfo{hasNextPage} nodes{id createdAt bodyText path line originalLine author{__typename login}}}}}
      comments(last:100){nodes{id createdAt bodyText author{__typename login}}}
    }}}`

func (ghPRReadinessSource) Fetch(ctx context.Context, opts prWaitOptions) (*prReadiness, error) {
	args := []string{"api", "graphql",
		"-f", "query=" + prSnapshotQuery,
		"-F", "owner=" + opts.Owner,
		"-F", "name=" + opts.Name,
		"-F", "number=" + strconv.Itoa(opts.Number),
	}
	if opts.Host != "" {
		args = append(args, "--hostname", opts.Host)
	}
	output, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("gh api graphql: %s", strings.TrimSpace(string(output)))
	}
	return parsePRSnapshot(output, opts)
}

type prGraphQLAuthor struct {
	TypeName string `json:"__typename"`
	Login    string `json:"login"`
}

type prGraphQLComment struct {
	ID           string          `json:"id"`
	CreatedAt    time.Time       `json:"createdAt"`
	BodyText     string          `json:"bodyText"`
	Path         string          `json:"path"`
	Line         *int            `json:"line"`
	OriginalLine *int            `json:"originalLine"`
	Author       prGraphQLAuthor `json:"author"`
}

// GitHub nulls an inline comment's line once its hunk is outdated; originalLine is
// then the only anchor left.
func (c prGraphQLComment) location() string {
	if c.Path == "" {
		return ""
	}
	line := c.Line
	if line == nil {
		line = c.OriginalLine
	}
	if line == nil {
		return c.Path
	}
	return fmt.Sprintf("%s:%d", c.Path, *line)
}

func parsePRSnapshot(output []byte, opts prWaitOptions) (*prReadiness, error) {
	var payload struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					Number         json.Number `json:"number"`
					State          string      `json:"state"`
					IsDraft        bool        `json:"isDraft"`
					HeadRefOID     string      `json:"headRefOid"`
					URL            string      `json:"url"`
					ReviewRequests struct {
						Nodes []struct {
							RequestedReviewer prGraphQLAuthor `json:"requestedReviewer"`
						} `json:"nodes"`
					} `json:"reviewRequests"`
					Commits struct {
						Nodes []struct {
							Commit struct {
								StatusCheckRollup *struct {
									Contexts struct {
										PageInfo struct {
											HasNextPage bool `json:"hasNextPage"`
										} `json:"pageInfo"`
										Nodes []struct {
											TypeName   string `json:"__typename"`
											Name       string `json:"name"`
											Context    string `json:"context"`
											Status     string `json:"status"`
											Conclusion string `json:"conclusion"`
											State      string `json:"state"`
											DetailsURL string `json:"detailsUrl"`
											TargetURL  string `json:"targetUrl"`
										} `json:"nodes"`
									} `json:"contexts"`
								} `json:"statusCheckRollup"`
							} `json:"commit"`
						} `json:"nodes"`
					} `json:"commits"`
					Reviews struct {
						Nodes []struct {
							ID          string          `json:"id"`
							State       string          `json:"state"`
							BodyText    string          `json:"bodyText"`
							SubmittedAt time.Time       `json:"submittedAt"`
							Author      prGraphQLAuthor `json:"author"`
							Commit      struct {
								OID string `json:"oid"`
							} `json:"commit"`
							Comments struct {
								PageInfo struct {
									HasNextPage bool `json:"hasNextPage"`
								} `json:"pageInfo"`
								Nodes []prGraphQLComment `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviews"`
					Comments struct {
						Nodes []prGraphQLComment `json:"nodes"`
					} `json:"comments"`
					ReviewThreads struct {
						Nodes []struct {
							Comments struct {
								Nodes []prGraphQLComment `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("parse gh api graphql: %w", err)
	}
	if len(payload.Errors) > 0 {
		return nil, fmt.Errorf("gh api graphql: %s", payload.Errors[0].Message)
	}
	pr := payload.Data.Repository.PullRequest
	if pr == nil || pr.Number == "" || pr.HeadRefOID == "" {
		return nil, errors.New("gh api graphql returned no PR number or head SHA")
	}

	result := &prReadiness{
		Number: pr.Number.String(), State: strings.ToLower(pr.State), Draft: pr.IsDraft,
		HeadSHA: pr.HeadRefOID, Reviewer: opts.Reviewer, ReviewState: "waiting",
		URL: pr.URL,
	}

	for _, request := range pr.ReviewRequests.Nodes {
		if request.RequestedReviewer.TypeName == "User" && strings.EqualFold(request.RequestedReviewer.Login, opts.Reviewer) {
			result.ReviewerRequested = true
			break
		}
	}

	if len(pr.Commits.Nodes) > 0 {
		if rollup := pr.Commits.Nodes[0].Commit.StatusCheckRollup; rollup != nil {
			if rollup.Contexts.PageInfo.HasNextPage {
				return nil, errors.New("PR has more than 100 checks; readiness cannot be verified without truncation")
			}
			for _, check := range rollup.Contexts.Nodes {
				name, state, url := "status:"+check.Context, statusState(check.State), check.TargetURL
				if check.TypeName == "CheckRun" {
					name, state, url = "check:"+check.Name, checkRunState(check.Status, check.Conclusion), check.DetailsURL
				}
				result.Checks = append(result.Checks, prCheck{Name: name, State: state, URL: url})
			}
		}
	}
	sort.Slice(result.Checks, func(i, j int) bool { return result.Checks[i].Name < result.Checks[j].Name })
	result.CheckState = summarizePRChecks(result.Checks)

	// Inline comments come from the reviews that carry them, not reviewThreads: a
	// reply to an old thread falls outside any newest-N slice of threads.
	var latest time.Time
	for _, review := range pr.Reviews.Nodes {
		state := strings.ToUpper(review.State)
		if strings.EqualFold(review.Author.Login, opts.Reviewer) && review.SubmittedAt.After(result.LatestReviewAt) {
			result.LatestReviewAt = review.SubmittedAt
		}
		// A review with no text of its own is GitHub's wrapper around an inline comment.
		if strings.TrimSpace(review.BodyText) != "" && !isTrackedReviewerVerdict(review.Author.Login, state, opts) {
			result.Comments = appendPRComment(result.Comments, prGraphQLComment{
				ID: review.ID, CreatedAt: review.SubmittedAt, Author: review.Author,
				BodyText: review.BodyText,
			}, "review", opts)
		}
		if review.Comments.PageInfo.HasNextPage {
			return nil, errors.New("a review carries more than 100 comments; new comments cannot be detected without truncation")
		}
		for _, comment := range review.Comments.Nodes {
			result.Comments = appendPRComment(result.Comments, comment, "inline", opts)
		}
		if state == "COMMENTED" {
			continue
		}
		if !strings.EqualFold(review.Author.Login, opts.Reviewer) || review.Commit.OID != result.HeadSHA ||
			(state != "APPROVED" && state != "CHANGES_REQUESTED") || review.SubmittedAt.Before(latest) {
			continue
		}
		latest = review.SubmittedAt
		result.ReviewSubmittedAt = review.SubmittedAt
		result.ReviewBody = strings.TrimSpace(review.BodyText)
		if state == "APPROVED" {
			result.ReviewState = "approved"
		} else {
			result.ReviewState = "changes_requested"
		}
	}
	for _, comment := range pr.Comments.Nodes {
		result.Comments = appendPRComment(result.Comments, comment, "issue", opts)
	}
	sort.Slice(result.Comments, func(i, j int) bool {
		return result.Comments[i].CreatedAt.Before(result.Comments[j].CreatedAt)
	})
	return result, nil
}

func appendPRComment(comments []prComment, node prGraphQLComment, kind string, opts prWaitOptions) []prComment {
	if node.ID == "" || opts.ignored(node.Author.Login) {
		return comments
	}
	return append(comments, prComment{
		ID:        node.ID,
		Author:    node.Author.Login,
		Kind:      kind,
		Bot:       node.Author.TypeName != "User",
		CreatedAt: node.CreatedAt,
		Body:      strings.TrimSpace(node.BodyText),
		Location:  node.location(),
	})
}

func checkRunState(status, conclusion string) string {
	if !strings.EqualFold(status, "COMPLETED") {
		return checksPending
	}
	switch strings.ToUpper(conclusion) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return checksGreen
	case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return checksFailed
	default:
		return checksPending
	}
}

func statusState(state string) string {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return checksGreen
	case "FAILURE", "ERROR":
		return checksFailed
	default:
		return checksPending
	}
}

func summarizePRChecks(checks []prCheck) string {
	if len(checks) == 0 {
		return checksNone
	}
	result := checksGreen
	for _, check := range checks {
		if check.State == checksFailed {
			return checksFailed
		}
		if check.State != checksGreen {
			result = checksPending
		}
	}
	return result
}

type prWaitResult struct {
	Observation *prReadiness
	Outcome     prOutcome
	Events      []prOutcome
	Cursor      prWaitCursor
}

func waitForPRActionable(ctx context.Context, source prReadinessSource, opts prWaitOptions, cursor prWaitCursor, progress io.Writer) (prWaitResult, error) {
	var lastLine, lastHead string
	var baseline map[string]bool
	var reviewBaseline time.Time
	var notedStaleVerdict bool
	last := &prReadiness{Number: strconv.Itoa(opts.Number), Reviewer: opts.Reviewer, CheckState: checksNone, ReviewState: "waiting"}
	if !opts.Since.IsZero() {
		cursor = prWaitCursor{VerdictAt: opts.Since}
		baseline = map[string]bool{}
		reviewBaseline = opts.Since
	} else if !cursor.empty() {
		baseline = cursor.seenComments()
		reviewBaseline = cursor.VerdictAt
	}

	for {
		observation, err := source.Fetch(ctx, opts)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return prWaitResult{Observation: last, Outcome: outcomeTimeout, Events: []prOutcome{outcomeTimeout}, Cursor: cursor}, nil
			}
			return prWaitResult{Observation: last}, err
		}
		last = observation

		if lastHead != "" && lastHead != observation.HeadSHA {
			fmt.Fprintf(progress, "head changed %s -> %s; reset\n", shortSHA(lastHead), shortSHA(observation.HeadSHA))
		}
		lastHead = observation.HeadSHA

		if line := readinessLine(observation); line != lastLine {
			fmt.Fprintln(progress, line)
			lastLine = line
		}

		if baseline == nil {
			baseline = make(map[string]bool, len(observation.Comments))
			for _, comment := range observation.Comments {
				baseline[comment.ID] = true
			}
			// The baseline goes into the cursor: without it a wait that times out reports
			// nothing and the next call re-baselines, swallowing what landed in between.
			cursor.CommentIDs = append(cursor.CommentIDs, prCommentIDs(observation.Comments)...)
			reviewBaseline = observation.LatestReviewAt
			observation.Comments = nil
		} else {
			observation.Comments = unseenPRComments(observation.Comments, baseline, opts.Since)
		}

		if !notedStaleVerdict && hasReviewVerdict(observation) && !freshReviewVerdict(observation, reviewBaseline) {
			fmt.Fprintf(progress, "%s %s predates the pending re-review request; waiting for a new review\n",
				observation.Reviewer, observation.ReviewState)
			notedStaleVerdict = true
		}

		var events []prOutcome
		if observation.State != "open" {
			events = append(events, outcomeClosed)
		}
		// A failing check is a condition, not an occurrence: without the same-failure
		// check a second wait returns instantly with nothing new.
		if observation.CheckState == checksFailed && !cursor.sameFailure(observation.HeadSHA, observation.Checks) {
			events = append(events, outcomeChecksFailed)
		}
		if freshReviewVerdict(observation, reviewBaseline) {
			switch {
			case observation.ReviewState == "changes_requested":
				events = append(events, outcomeChangesRequested)
			case observation.ready():
				events = append(events, outcomeApproved)
			}
		}
		if len(humanPRComments(observation.Comments)) > 0 {
			events = append(events, outcomeComment)
		}
		if len(botPRComments(observation.Comments)) > 0 {
			events = append(events, outcomeBotComment)
		}
		if winner, ranked := rankPROutcomes(events); winner != "" {
			return prWaitResult{
				Observation: observation,
				Outcome:     winner,
				Events:      ranked,
				Cursor:      advancePRWaitCursor(cursor, observation, ranked),
			}, nil
		}

		if err := waitPRPoll(ctx, opts.Interval); err != nil {
			return prWaitResult{Observation: observation, Outcome: outcomeTimeout, Events: []prOutcome{outcomeTimeout}, Cursor: cursor}, nil
		}
	}
}

func freshReviewVerdict(observation *prReadiness, baseline time.Time) bool {
	if !observation.ReviewerRequested {
		return true
	}
	return observation.ReviewSubmittedAt.After(baseline)
}

func unseenPRComments(comments []prComment, baseline map[string]bool, since time.Time) []prComment {
	var fresh []prComment
	for _, comment := range comments {
		if baseline[comment.ID] {
			continue
		}
		if !since.IsZero() && !comment.CreatedAt.After(since) {
			continue
		}
		fresh = append(fresh, comment)
	}
	return fresh
}

func prCommentIDs(comments []prComment) []string {
	ids := make([]string, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.ID)
	}
	return ids
}

func advancePRWaitCursor(cursor prWaitCursor, observation *prReadiness, events []prOutcome) prWaitCursor {
	reported := make(map[prOutcome]bool, len(events))
	for _, event := range events {
		reported[event] = true
	}
	if reported[outcomeComment] || reported[outcomeBotComment] {
		cursor.CommentIDs = append(cursor.CommentIDs, prCommentIDs(observation.Comments)...)
	}
	if reported[outcomeApproved] || reported[outcomeChangesRequested] {
		cursor.VerdictAt = observation.ReviewSubmittedAt
	}
	if reported[outcomeChecksFailed] {
		cursor.FailureHead = observation.HeadSHA
		cursor.FailureChecks = failedCheckNames(observation.Checks)
	}
	return cursor
}

func waitPRPoll(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readinessLine(r *prReadiness) string {
	parts := make([]string, 0, len(r.Checks))
	for _, check := range r.Checks {
		parts = append(parts, check.Name+"="+check.State)
	}
	checks := "-"
	if len(parts) > 0 {
		checks = strings.Join(parts, ",")
	}
	line := fmt.Sprintf("pr=#%s head=%s state=%s draft=%t checks=%s [%s] review=%s reviewer=%s",
		r.Number, shortSHA(r.HeadSHA), r.State, r.Draft, r.CheckState, checks, r.ReviewState, r.Reviewer)
	if r.ReviewerRequested {
		line += " re-requested=true"
	}
	return line
}

func hasReviewVerdict(r *prReadiness) bool {
	return r.ReviewState == "approved" || r.ReviewState == "changes_requested"
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func writePRHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn pr <command>

commands:
  wait-ready <number-or-url> --reviewer <login>   wait for an actionable update
  record <url> [--session <id>]                   record a pull request this session opened
  ls [--session <id>] [--json]                    list the pull requests a session opened
  forget <url> [--session <id>]                   drop one from the session's list

record/ls/forget default to the session in ATTN_SESSION_ID. Claude Code and Codex
sessions record their own "gh pr create" through the tool-use hook, and pi sessions
through the driver; record is the way in for every other harness and for a pull
request opened by hand. Recording the same pull request twice is a no-op, so a
double report costs nothing.

attn pr wait-ready <number-or-url> --reviewer <login> [options]

Wait until a pull request has an actionable update: it closes, a check fails, the
reviewer requests changes, a human comments, a bot comments, or the reviewer
approves a green exact head.

options:
  --repo [host/]owner/repository  required with a pull request number
  --reviewer login                required reviewer
  --timeout duration              maximum wait (default 30m)
  --interval duration             poll interval (default 20s)
  --ignore-author login           comment author to ignore (repeatable)
  --json                          emit the result as JSON on stdout
  --reset                         forget earlier waits; baseline from now
  --since RFC3339                 report anything after this instant instead
  --include-self                  report your own comments as events

One poll can see several of these at once. The exit code reports the highest
ranked: closed, checks failed, changes requested, human comment, approved, bot
comment. A human comment outranks approval because someone is waiting for an
answer; a bot comment ranks last because nobody is. Every event that poll saw is
still reported — "also <event>: ..." on stdout, "events" in --json — so an
approval that arrives alongside a comment is never lost to the one the exit code
names. The reviewer's own approval or changes-requested is one event, not two:
its body is the verdict's explanation, not a separate comment.

A bot comment ends the wait with its own exit code, so a caller can act on a
human's remark and skip a doctor report; --ignore-author drops either kind.

Your own comments are not events. The account gh is authenticated as is resolved
once per run and its remarks are dropped, because the caller of a wait is the one
who just acted and being told about your own comment is never the update you were
waiting for. The baseline does not cover this on its own: a comment posted
between two waits is new to the second one. Pass --include-self to watch a pull
request you also comment on. If the login cannot be resolved, the wait runs
exactly as it would without this, reporting everyone.

Comments already present when the wait starts are the baseline and never
reported; only comments posted during the wait are. A review verdict present at
wait start is likewise baselined: while the reviewer is re-requested (a re-review
is pending) the pre-existing verdict is stale and does not end the wait; only a
review submitted after the baseline does. When the reviewer is not re-requested,
an existing verdict returns immediately.

Successive waits on the same pull request resume rather than re-baseline. Each
wait records what it reported under the data dir, so a remark that lands while the
caller is answering the previous one is still reported by the next wait instead of
being absorbed into a fresh baseline. The same memory keeps a failing check from
returning instantly a second time for the same checks on the same commit; a
different failure, or the same one on a new commit, is reported again. --json
echoes the recorded position; --reset discards it and --since replays from an
instant of your choosing.

Also printed on stdout: comment bodies with their file:line when inline, the
verdict's own text, failing check names with their URLs, and the pull request URL
— so acting on the result needs no second query.

exit: 0 approved; 1 checks failed; 2 usage; 3 changes requested; 4 human comment;
      5 error; 6 bot comment; 124 timeout
`)
}
