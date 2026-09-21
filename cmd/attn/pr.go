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
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/prreadiness"
)

const (
	prWaitExitReady            = 0
	prWaitExitChecksFailed     = 1
	prWaitExitUsage            = 2
	prWaitExitChangesRequested = 3
	prWaitExitFeedback         = 4
	prWaitExitError            = 5
	prWaitExitTimeout          = 124
)

type prOutcome string

const (
	outcomeReady            prOutcome = "ready"
	outcomeChecksFailed     prOutcome = "checks_failed"
	outcomeChangesRequested prOutcome = "changes_requested"
	outcomeComment          prOutcome = "comment"
	outcomeReply            prOutcome = "reply"
	outcomeThreadReopened   prOutcome = "thread_reopened"
	outcomeMonitoringOutage prOutcome = "monitoring_outage"
	outcomeClosed           prOutcome = "closed"
	outcomeMerged           prOutcome = "merged"
	outcomeTimeout          prOutcome = "timeout"
)

var prOutcomeRanking = []prOutcome{
	outcomeMerged,
	outcomeClosed,
	outcomeChecksFailed,
	outcomeChangesRequested,
	outcomeComment,
	outcomeReply,
	outcomeThreadReopened,
	outcomeReady,
	outcomeMonitoringOutage,
}

func (outcome prOutcome) exitCode() int {
	switch outcome {
	case outcomeReady:
		return prWaitExitReady
	case outcomeChecksFailed:
		return prWaitExitChecksFailed
	case outcomeChangesRequested:
		return prWaitExitChangesRequested
	case outcomeComment, outcomeReply, outcomeThreadReopened:
		return prWaitExitFeedback
	case outcomeTimeout:
		return prWaitExitTimeout
	default:
		return prWaitExitError
	}
}

type prWaitOptions struct {
	Host, Owner, Name string
	Number            int
	Mode              prreadiness.Mode
	Reviewer          string
	IgnoreAuthors     []string
	Timeout           time.Duration
	Interval          time.Duration
	JSON              bool
	CursorDir         string
	Since             time.Time
	Reset             bool
}

func (opts prWaitOptions) repo() string { return opts.Owner + "/" + opts.Name }

func (opts prWaitOptions) ignored(author string) bool {
	for _, ignored := range opts.IgnoreAuthors {
		if strings.EqualFold(strings.TrimSpace(ignored), strings.TrimSpace(author)) {
			return true
		}
	}
	return false
}

type stringSliceFlag []string

func (values *stringSliceFlag) String() string { return strings.Join(*values, ",") }

func (values *stringSliceFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("author must not be empty")
	}
	*values = append(*values, value)
	return nil
}

type prSource interface {
	Readiness(context.Context, prWaitOptions) (*prreadiness.Observation, error)
	Feedback(context.Context, prWaitOptions) ([]prreadiness.FeedbackItem, []prreadiness.ThreadState, error)
}

type ghPRSource struct {
	transport github.QueryTransport
}

func (source ghPRSource) Readiness(ctx context.Context, opts prWaitOptions) (*prreadiness.Observation, error) {
	return github.FetchPullRequestReadiness(ctx, source.transport, opts.repo(), opts.Number)
}

func (source ghPRSource) Feedback(ctx context.Context, opts prWaitOptions) ([]prreadiness.FeedbackItem, []prreadiness.ThreadState, error) {
	return github.FetchPullRequestFeedback(ctx, source.transport, opts.repo(), opts.Number)
}

type ghCommandTransport struct {
	host string
}

func (transport ghCommandTransport) GraphQL(ctx context.Context, query string, variables map[string]any) ([]byte, error) {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for _, key := range []string{"owner", "name", "number", "cursor", "id"} {
		value, ok := variables[key]
		if !ok {
			continue
		}
		args = append(args, "-F", key+"="+fmt.Sprint(value))
	}
	if transport.host != "" && transport.host != "github.com" {
		args = append(args, "--hostname", transport.host)
	}
	output, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, fmt.Errorf("gh api graphql: %s", strings.TrimSpace(string(output)))
	}
	return output, nil
}

type prWaitResult struct {
	Outcome            prOutcome
	Outcomes           []prOutcome
	Actions            []prreadiness.Action
	Observation        *prreadiness.Observation
	Evaluation         prreadiness.Evaluation
	Health             string
	HealthError        string
	Cursor             prWaitCursor
	BeforeOutputCursor prWaitCursor
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
	if len(args) > 0 {
		switch args[0] {
		case "record", "forget", "ls", "watch", "unwatch", "status":
			return executeSessionPRCommand(args[0], args[1:], stdout, stderr)
		}
	}
	if len(args) == 0 || args[0] != "wait-ready" {
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
	opts.CursorDir = filepath.Join(config.DataDir(), "pr-wait")
	cursor := prWaitCursor{}
	if !opts.Reset && opts.Since.IsZero() {
		loaded, loadErr := loadPRWaitCursor(opts.CursorDir, opts)
		if loadErr != nil {
			fmt.Fprintf(stderr, "pr wait-ready: %v; starting from the current state\n", loadErr)
		} else {
			cursor = loaded
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	progress := stdout
	if opts.JSON {
		progress = stderr
	}
	source := ghPRSource{transport: ghCommandTransport{host: opts.Host}}
	result := waitForPRActionable(ctx, source, opts, cursor, progress)
	if err := savePRWaitCursor(opts.CursorDir, opts, result.BeforeOutputCursor, time.Now()); err != nil {
		fmt.Fprintf(stderr, "pr wait-ready: could not save cursor: %v\n", err)
	}
	code := reportPROutcome(result, opts, stdout)
	if err := savePRWaitCursor(opts.CursorDir, opts, result.Cursor, time.Now()); err != nil {
		fmt.Fprintf(stderr, "pr wait-ready: could not acknowledge cursor: %v\n", err)
	}
	return code
}

func waitForPRActionable(ctx context.Context, source prSource, opts prWaitOptions, cursor prWaitCursor, progress io.Writer) prWaitResult {
	var last prWaitResult
	for {
		now := time.Now().UTC()
		step := observePR(ctx, source, opts, cursor, now)
		cursor = step.Cursor
		last = step
		if step.Outcome != "" {
			return step
		}
		line := readinessLine(step)
		if line != "" {
			fmt.Fprintln(progress, line)
		}
		wait := opts.Interval
		if deadline := step.Evaluation.SettlingUntil; deadline != nil {
			if until := time.Until(*deadline); until > 0 && until < wait {
				wait = until
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			last.Outcome = outcomeTimeout
			last.Outcomes = []prOutcome{outcomeTimeout}
			last.BeforeOutputCursor = last.Cursor
			return last
		case <-timer.C:
		}
	}
}

func observePR(ctx context.Context, source prSource, opts prWaitOptions, cursor prWaitCursor, now time.Time) prWaitResult {
	original := cursor
	if cursor.Mode != opts.Mode || !strings.EqualFold(cursor.Reviewer, opts.Reviewer) {
		cursor.Readiness = prreadiness.PreserveFeedbackCursor(cursor.Readiness)
		cursor.Mode, cursor.Reviewer = opts.Mode, opts.Reviewer
	}
	observation, err := source.Readiness(ctx, opts)
	if err != nil {
		result := prWaitResult{Health: "delayed", HealthError: err.Error(), Cursor: cursor, BeforeOutputCursor: original}
		if !cursor.OutageActive {
			result.Outcome = outcomeMonitoringOutage
			result.Outcomes = []prOutcome{outcomeMonitoringOutage}
			result.Cursor.OutageActive = true
		}
		return result
	}
	cursor.OutageActive = false
	feedback, threads, feedbackErr := source.Feedback(ctx, opts)
	if feedbackErr == nil {
		filtered := feedback[:0]
		for _, item := range feedback {
			if !opts.ignored(item.Author) {
				filtered = append(filtered, item)
			}
		}
		observation.Feedback = filtered
		observation.Threads = threads
		observation.FeedbackComplete = true
	}
	if (!cursor.Readiness.Initialized || cursor.Readiness.FeedbackBaselinePending) && !opts.Since.IsZero() && observation.FeedbackComplete {
		cursor.Readiness.Initialized = true
		cursor.Readiness.FeedbackBaselinePending = false
		cursor.Readiness.ThreadStates = make(map[string]bool, len(observation.Threads))
		for _, item := range observation.Feedback {
			if !item.CreatedAt.After(opts.Since) {
				cursor.Readiness.SeenFeedbackIDs = append(cursor.Readiness.SeenFeedbackIDs, item.ID)
			}
		}
		for _, thread := range observation.Threads {
			cursor.Readiness.ThreadStates[thread.ID] = thread.Resolved
		}
	}
	beforeReadiness := cursor.Readiness
	transition := prreadiness.Advance(cursor.Readiness, *observation, opts.Mode, opts.Reviewer, now)
	cursor.Readiness = transition.Cursor
	before := cursor
	before.Readiness = beforeReadiness
	before.Readiness.HeadSHA = transition.Cursor.HeadSHA
	before.Readiness.HeadObservedAt = transition.Cursor.HeadObservedAt

	outcomes := rankedOutcomes(transition.Actions)
	result := prWaitResult{
		Actions: transition.Actions, Observation: observation, Evaluation: transition.Evaluation,
		Health: "ok", Cursor: cursor, BeforeOutputCursor: before, Outcomes: outcomes,
	}
	if feedbackErr != nil {
		result.Health, result.HealthError = "delayed", feedbackErr.Error()
	}
	if len(outcomes) > 0 {
		result.Outcome = outcomes[0]
	}
	return result
}

func rankedOutcomes(actions []prreadiness.Action) []prOutcome {
	present := make(map[prOutcome]bool)
	for _, action := range actions {
		outcome := outcomeForAction(action.Kind)
		if outcome != "" {
			present[outcome] = true
		}
	}
	result := make([]prOutcome, 0, len(present))
	for _, outcome := range prOutcomeRanking {
		if present[outcome] {
			result = append(result, outcome)
		}
	}
	return result
}

func outcomeForAction(kind string) prOutcome {
	switch kind {
	case "ready":
		return outcomeReady
	case "checks_failed":
		return outcomeChecksFailed
	case "changes_requested":
		return outcomeChangesRequested
	case "comment", "inline_comment", "review":
		return outcomeComment
	case "reply":
		return outcomeReply
	case "thread_reopened":
		return outcomeThreadReopened
	case "closed":
		return outcomeClosed
	case "merged":
		return outcomeMerged
	case "monitoring_outage":
		return outcomeMonitoringOutage
	default:
		return ""
	}
}

func reportPROutcome(result prWaitResult, opts prWaitOptions, stdout io.Writer) int {
	if opts.JSON {
		payload := map[string]any{
			"outcome": result.Outcome, "events": result.Outcomes, "mode": opts.Mode,
			"state": result.Evaluation.State, "reason": result.Evaluation.Reason,
			"description": result.Evaluation.Description, "health": result.Health,
			"health_error": result.HealthError, "settling_until": result.Evaluation.SettlingUntil,
			"actions": result.Actions, "cursor": result.Cursor,
		}
		if result.Observation != nil {
			payload["pr"], payload["url"], payload["head"] = result.Observation.Number, result.Observation.URL, result.Observation.HeadSHA
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(payload); err != nil {
			return prWaitExitError
		}
		return result.Outcome.exitCode()
	}
	detail := result.Evaluation.Description
	if result.Outcome == outcomeTimeout {
		detail = fmt.Sprintf("no actionable update after %s", opts.Timeout)
	}
	if result.Outcome == outcomeMonitoringOutage {
		detail = result.HealthError
	}
	if detail == "" {
		detail = string(result.Outcome)
	}
	fmt.Fprintf(stdout, "%s: %s\n", result.Outcome, detail)
	for _, action := range result.Actions {
		if feedback := action.Feedback; feedback != nil {
			where := feedback.Author
			if feedback.Location != "" {
				where += " on " + feedback.Location
			}
			fmt.Fprintf(stdout, "  %s: %s\n", where, strings.TrimSpace(feedback.Body))
			continue
		}
		for _, detail := range action.Details {
			fmt.Fprintf(stdout, "  %s\n", detail)
		}
	}
	if result.Observation != nil && result.Observation.URL != "" {
		fmt.Fprintln(stdout, result.Observation.URL)
	}
	return result.Outcome.exitCode()
}

func readinessLine(result prWaitResult) string {
	if result.Observation == nil {
		return ""
	}
	line := fmt.Sprintf("%s %s: %s", shortSHA(result.Observation.HeadSHA), result.Evaluation.State, result.Evaluation.Description)
	if result.Health == "delayed" {
		line += " (feedback delayed: " + result.HealthError + ")"
	}
	return line
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func parsePRWaitArgs(args []string) (prWaitOptions, error) {
	fs := flag.NewFlagSet("pr wait-ready", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "[host/]owner/repository")
	modeValue := fs.String("mode", string(prreadiness.ModeGreen), "green, codex, or formal-review")
	reviewer := fs.String("reviewer", "", "optional reviewer login for formal-review")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum wait")
	interval := fs.Duration("interval", 20*time.Second, "poll interval")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	reset := fs.Bool("reset", false, "forget what earlier waits reported and baseline from the current state")
	since := fs.String("since", "", "report feedback after this RFC3339 instant instead of resuming")
	var ignore stringSliceFlag
	fs.Var(&ignore, "ignore-author", "feedback author to ignore (repeatable)")

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
		return prWaitOptions{}, errors.New("usage: attn pr wait-ready <number-or-url> [--repo [host/]owner/repository]")
	}
	if target == "" {
		return prWaitOptions{}, errors.New("pull request target is required")
	}
	mode, err := prreadiness.ParseMode(*modeValue)
	if err != nil {
		return prWaitOptions{}, err
	}
	if err := prreadiness.ValidateConfig(mode, *reviewer); err != nil {
		return prWaitOptions{}, err
	}
	if *timeout <= 0 || *interval <= 0 {
		return prWaitOptions{}, errors.New("--timeout and --interval must be positive")
	}
	opts := prWaitOptions{Mode: mode, Reviewer: strings.TrimSpace(*reviewer), IgnoreAuthors: ignore, Timeout: *timeout, Interval: *interval, JSON: *asJSON, Reset: *reset}
	if strings.TrimSpace(*since) != "" {
		opts.Since, err = time.Parse(time.RFC3339, strings.TrimSpace(*since))
		if err != nil {
			return prWaitOptions{}, fmt.Errorf("--since must be an RFC3339 timestamp: %w", err)
		}
	}
	if strings.HasPrefix(target, "https://") {
		opts.Host, opts.Owner, opts.Name, opts.Number, err = automation.ParsePullRequestURL(target)
		return opts, err
	}
	opts.Number, err = strconv.Atoi(target)
	if err != nil || opts.Number <= 0 {
		return prWaitOptions{}, errors.New("pull request number must be positive")
	}
	if strings.TrimSpace(*repo) == "" {
		return prWaitOptions{}, errors.New("--repo is required when the target is a number")
	}
	opts.Host, opts.Owner, opts.Name, err = parseRepoFlag(*repo)
	return opts, err
}

func parseRepoFlag(repo string) (host, owner, name string, err error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(repo), "/"), "/")
	switch len(parts) {
	case 2:
		return "github.com", parts[0], parts[1], nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", errors.New("--repo must be [host/]owner/repository")
	}
}

func isHelpArg(arg string) bool { return arg == "-h" || arg == "--help" }

func writePRHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn pr <command>

commands:
  record <url> [--session <id>]                         record a pull request opened by a session
  ls [--session <id>] [--json]                         list a session's pull requests and watch status
  forget <url> [--session <id>]                        stop recording a pull request
  watch <url> [--session <id>] [--mode <mode>]         durably watch readiness and feedback
  unwatch <url> [--session <id>]                       stop a durable watch
  status [<url>] [--session <id>] [--json]             show recorded watch/readiness state
  wait-ready <number-or-url> [options]                 wait for the next actionable update

wait-ready options:
  --repo [host/]owner/repository  required when the target is a number
  --mode green|codex|formal-review
  --reviewer login                optional only with formal-review
  --timeout duration              default 30m
  --interval duration             default 20s; Codex deadlines wake independently
  --since RFC3339                 report feedback after this time instead of resuming
  --reset                         baseline feedback from the current state
  --ignore-author login           repeatable
  --json                          emit one machine-readable result
`)
}
