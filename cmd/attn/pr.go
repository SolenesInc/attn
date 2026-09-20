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
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/prreadiness"
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

	checksNone    = prreadiness.ChecksNone
	checksPending = prreadiness.ChecksPending
	checksGreen   = prreadiness.ChecksGreen
	checksFailed  = prreadiness.ChecksFailed
)

type prOutcome string

const (
	outcomeApproved          prOutcome = "approved"
	outcomeChecksFailed      prOutcome = "checks_failed"
	outcomeChangesRequested  prOutcome = "changes_requested"
	outcomeComment           prOutcome = "comment"
	outcomeBotComment        prOutcome = "bot_comment"
	outcomeReviewUnavailable prOutcome = "review_unavailable"
	outcomeClosed            prOutcome = "closed"
	outcomeTimeout           prOutcome = "timeout"
)

var prOutcomeRanking = []prOutcome{
	outcomeClosed,
	outcomeChecksFailed,
	outcomeChangesRequested,
	outcomeReviewUnavailable,
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
	case outcomeReviewUnavailable:
		return prWaitExitError
	case outcomeTimeout:
		return prWaitExitTimeout
	default:
		return prWaitExitError
	}
}

type prCheck struct {
	Name  string                 `json:"name"`
	State prreadiness.CheckState `json:"state"`
	URL   string                 `json:"url,omitempty"`
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
	if !prreadiness.SameActor(author, opts.Reviewer) {
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
	Number, State, HeadSHA, Reviewer string
	CheckState                       prreadiness.CheckState
	ReviewState                      prreadiness.ReviewState
	Draft                            bool
	Checks                           []prCheck
	Comments                         []prComment
	ReviewerRequested                bool
	ReviewSubmittedAt                time.Time
	ReviewSignalID                   string
	ReviewBody                       string
	URL                              string
	Ready                            bool
	observation                      prreadiness.Observation
}

func (r *prReadiness) applyEvaluation(evaluation prreadiness.Evaluation) {
	r.ReviewState = evaluation.ReviewState
	r.ReviewBody = evaluation.ReviewBody
	r.ReviewSubmittedAt = evaluation.ReviewSubmitted
	r.ReviewSignalID = evaluation.SignalID
	r.Ready = evaluation.Ready
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
	persistCursor     func(prreadiness.Cursor) error
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

type ghQueryTransport struct {
	host string
}

func (t ghQueryTransport) GraphQL(ctx context.Context, query string, variables map[string]any) ([]byte, error) {
	args := []string{"api", "graphql", "-f", "query=" + query}
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := variables[key]
		if value == nil || value == "" {
			continue
		}
		args = append(args, "-F", fmt.Sprintf("%s=%v", key, value))
	}
	if t.host != "" {
		args = append(args, "--hostname", t.host)
	}
	output, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("gh api graphql: %s", strings.TrimSpace(string(output)))
	}
	return output, nil
}

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
	case len(args) > 0 && (args[0] == "record" || args[0] == "forget" || args[0] == "ls" ||
		args[0] == "watch" || args[0] == "unwatch" || args[0] == "status"):
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
	opts.persistCursor = func(next prreadiness.Cursor) error {
		cursor.Cursor = next
		return savePRWaitCursor(opts.CursorDir, opts, cursor, time.Now())
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
	if (reported[outcomeChangesRequested] || reported[outcomeApproved] || reported[outcomeReviewUnavailable]) && result.ReviewBody != "" {
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
		if result.ReviewState == prreadiness.ReviewUnresolved {
			return fmt.Sprintf("unresolved review threads block %s", head)
		}
		return fmt.Sprintf("%s requested changes on %s", result.Reviewer, head)
	case outcomeChecksFailed:
		return fmt.Sprintf("%s failed on %s", strings.Join(failedCheckNames(result.Checks), ", "), head)
	case outcomeComment:
		return describePRComments(humanPRComments(result.Comments))
	case outcomeBotComment:
		return describePRComments(botPRComments(result.Comments))
	case outcomeClosed:
		return fmt.Sprintf("pull request is %s", result.State)
	case outcomeReviewUnavailable:
		return fmt.Sprintf("%s review is unavailable for %s", result.Reviewer, head)
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

func (ghPRReadinessSource) Fetch(ctx context.Context, opts prWaitOptions) (*prReadiness, error) {
	observation, err := github.FetchPullRequestReadiness(
		ctx, ghQueryTransport{host: opts.Host}, opts.Owner+"/"+opts.Name, opts.Number,
	)
	if err != nil {
		return nil, err
	}
	return readinessForCLI(*observation, opts), nil
}

func readinessForCLI(observation prreadiness.Observation, opts prWaitOptions) *prReadiness {
	result := &prReadiness{
		Number: strconv.Itoa(observation.Number), State: observation.State, Draft: observation.Draft,
		HeadSHA: observation.HeadSHA, Reviewer: opts.Reviewer, URL: observation.URL,
		CheckState: observation.CheckState, observation: observation,
	}
	for _, reviewer := range observation.RequestedReviewers {
		if prreadiness.SameActor(reviewer, opts.Reviewer) {
			result.ReviewerRequested = true
			break
		}
	}
	for _, check := range observation.Checks {
		result.Checks = append(result.Checks, prCheck{Name: check.Name, State: check.State, URL: check.URL})
	}
	sort.Slice(result.Checks, func(i, j int) bool { return result.Checks[i].Name < result.Checks[j].Name })
	for _, comment := range observation.Comments {
		if comment.ID == "" || opts.ignored(comment.Author) ||
			(comment.Kind == "review" && isTrackedReviewerVerdict(comment.Author, comment.ReviewState, opts)) {
			continue
		}
		result.Comments = append(result.Comments, prComment{
			ID: comment.ID, Author: comment.Author, Kind: comment.Kind, Bot: comment.Bot,
			CreatedAt: comment.CreatedAt, Body: strings.TrimSpace(comment.Body), Location: comment.Location,
		})
	}
	evaluation := prreadiness.Evaluate(observation, opts.Reviewer, nil)
	result.applyEvaluation(evaluation)
	sort.Slice(result.Comments, func(i, j int) bool {
		return result.Comments[i].CreatedAt.Before(result.Comments[j].CreatedAt)
	})
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
	var notedStaleVerdict bool
	last := &prReadiness{Number: strconv.Itoa(opts.Number), Reviewer: opts.Reviewer, CheckState: checksNone, ReviewState: "waiting"}
	if !opts.Since.IsZero() {
		cursor.Cursor = prreadiness.Cursor{}
	}
	ignored := append([]string(nil), opts.IgnoreAuthors...)
	if opts.SelfLogin != "" {
		ignored = append(ignored, opts.SelfLogin)
	}
	policy := prreadiness.StartPolicy{
		Since: opts.Since, HoldExistingVerdictWhenRequested: true, IgnoreAuthors: ignored,
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
		transition := prreadiness.Advance(cursor.Cursor, observation.observation, opts.Reviewer, policy)
		if (!cursor.Initialized || transition.HeadChanged || transition.ReviewerChanged) && opts.persistCursor != nil {
			if err := opts.persistCursor(transition.BaselineCursor); err != nil {
				return prWaitResult{Observation: last}, fmt.Errorf("save readiness baseline: %w", err)
			}
		}
		cursor.Cursor = transition.BaselineCursor
		observation.applyEvaluation(transition.Evaluation)
		outcomes, comments := cliTransitionEvents(transition.Events)
		observation.Comments = comments

		if lastHead != "" && lastHead != observation.HeadSHA {
			fmt.Fprintf(progress, "head changed %s -> %s; reset\n", shortSHA(lastHead), shortSHA(observation.HeadSHA))
		}
		lastHead = observation.HeadSHA

		if line := readinessLine(observation); line != lastLine {
			fmt.Fprintln(progress, line)
			lastLine = line
		}

		if !notedStaleVerdict && observation.ReviewerRequested && hasReviewVerdict(observation) &&
			observation.ReviewSignalID != "" && containsString(transition.BaselineCursor.SeenVerdictIDs, observation.ReviewSignalID) {
			fmt.Fprintf(progress, "%s %s predates the pending re-review request; waiting for a new review\n",
				observation.Reviewer, observation.ReviewState)
			notedStaleVerdict = true
		}

		if winner, ranked := rankPROutcomes(outcomes); winner != "" {
			cursor.Cursor = transition.NextCursor
			return prWaitResult{
				Observation: observation,
				Outcome:     winner,
				Events:      ranked,
				Cursor:      cursor,
			}, nil
		}
		cursor.Cursor = transition.NextCursor
		if opts.persistCursor != nil {
			if err := opts.persistCursor(cursor.Cursor); err != nil {
				return prWaitResult{Observation: last}, fmt.Errorf("save readiness cursor: %w", err)
			}
		}

		if err := waitPRPoll(ctx, opts.Interval); err != nil {
			return prWaitResult{Observation: observation, Outcome: outcomeTimeout, Events: []prOutcome{outcomeTimeout}, Cursor: cursor}, nil
		}
	}
}

func cliTransitionEvents(events []prreadiness.Event) ([]prOutcome, []prComment) {
	var outcomes []prOutcome
	var comments []prComment
	for _, event := range events {
		for _, outcome := range event.Outcomes {
			outcomes = append(outcomes, prOutcome(outcome))
		}
		for _, comment := range event.Comments {
			comments = append(comments, prComment{
				ID: comment.ID, Author: comment.Author, Kind: comment.Kind, Bot: comment.Bot,
				CreatedAt: comment.CreatedAt, Body: strings.TrimSpace(comment.Body), Location: comment.Location,
			})
		}
	}
	sort.Slice(comments, func(i, j int) bool { return comments[i].CreatedAt.Before(comments[j].CreatedAt) })
	return outcomes, comments
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
		parts = append(parts, check.Name+"="+string(check.State))
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
	return r.ReviewState == "approved" || r.ReviewState == "changes_requested" || r.ReviewState == prreadiness.ReviewUnresolved
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
  watch <url> [--reviewer <login>]                arm durable readiness monitoring
  unwatch <url>                                   stop this session's monitor
  status [url]                                    show this session's monitors
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
reviewer requests changes, review becomes unavailable, a human comments, a bot
comments, or the reviewer approves a green exact head.

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
