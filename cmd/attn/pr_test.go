package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/prreadiness"
)

type fakeReadinessSource struct {
	results []*prReadiness
	calls   int
	after   error
}

func (f *fakeReadinessSource) Fetch(context.Context, prWaitOptions) (*prReadiness, error) {
	index := f.calls
	f.calls++
	if index >= len(f.results) {
		if f.after != nil {
			return nil, f.after
		}
		index = len(f.results) - 1
	}
	return f.results[index], nil
}

func cliObservation(observation prreadiness.Observation, reviewer string) *prReadiness {
	return readinessForCLI(observation, prWaitOptions{Reviewer: reviewer})
}

func TestWaitForPRActionableUsesSharedTransitionAndPersistsBaseline(t *testing.T) {
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	first := prreadiness.Observation{
		Number: 303, URL: "https://github.com/SolenesInc/attn/pull/303", State: "open", HeadSHA: "head",
		MergeableState: "clean", CheckState: prreadiness.ChecksPending,
		RequestedReviewers: []string{"reviewer"},
		Comments:           []prreadiness.Comment{{ID: "existing", Author: "human", CreatedAt: at}},
	}
	second := first
	second.Comments = append(second.Comments, prreadiness.Comment{
		ID: "new", Author: "human", Body: "Please fix this", CreatedAt: at.Add(time.Minute),
	})
	var persisted []prreadiness.Cursor
	opts := prWaitOptions{
		Number: 303, Reviewer: "reviewer", Interval: 0,
		persistCursor: func(cursor prreadiness.Cursor) error {
			persisted = append(persisted, cursor)
			return nil
		},
	}
	result, err := waitForPRActionable(context.Background(), &fakeReadinessSource{
		results: []*prReadiness{cliObservation(first, opts.Reviewer), cliObservation(second, opts.Reviewer)},
	}, opts, prWaitCursor{}, io.Discard)
	if err != nil || result.Outcome != outcomeComment || len(result.Observation.Comments) != 1 ||
		result.Observation.Comments[0].ID != "new" {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
	if len(persisted) == 0 || !persisted[0].Initialized || !containsString(persisted[0].SeenCommentIDs, "existing") ||
		containsString(persisted[0].SeenCommentIDs, "new") {
		t.Fatalf("baseline was not persisted before delivery: %+v", persisted)
	}
	if !containsString(result.Cursor.SeenCommentIDs, "new") {
		t.Fatalf("next cursor did not advance delivered feedback: %+v", result.Cursor)
	}
}

func TestWaitForPRActionableRanksConcurrentEvents(t *testing.T) {
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	observation := prreadiness.Observation{
		Number: 303, State: "open", HeadSHA: "head", MergeableState: "clean",
		CheckState: prreadiness.ChecksFailed,
		Checks:     []prreadiness.Check{{Name: "CI", State: prreadiness.ChecksFailed}},
		Comments:   []prreadiness.Comment{{ID: "new", Author: "human", CreatedAt: at.Add(time.Minute)}},
	}
	cursor := prWaitCursor{Cursor: prreadiness.Cursor{
		Initialized: true, Reviewer: "reviewer", HeadSHA: "head", SeenCommentIDs: []string{"old"},
	}}
	result, err := waitForPRActionable(
		context.Background(), &fakeReadinessSource{results: []*prReadiness{cliObservation(observation, "reviewer")}},
		prWaitOptions{Number: 303, Reviewer: "reviewer"}, cursor, io.Discard,
	)
	if err != nil || result.Outcome != outcomeChecksFailed ||
		strings.Join(outcomesToStrings(result.Events), ",") != "checks_failed,comment" {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
}

func outcomesToStrings(outcomes []prOutcome) []string {
	values := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		values = append(values, string(outcome))
	}
	return values
}

func TestReportPROutcomeWritesPlainTextAndJSON(t *testing.T) {
	observation := cliObservation(prreadiness.Observation{
		Number: 303, URL: "https://github.com/SolenesInc/attn/pull/303", State: "open", HeadSHA: "abcdef1234567890",
		CheckState: prreadiness.ChecksFailed,
		Checks:     []prreadiness.Check{{Name: "check:CI", State: prreadiness.ChecksFailed, URL: "https://example.test/ci"}},
	}, "reviewer")
	wait := prWaitResult{Observation: observation, Outcome: outcomeChecksFailed, Events: []prOutcome{outcomeChecksFailed}}
	var plain bytes.Buffer
	if code := reportPROutcome(wait, prWaitOptions{}, &plain); code != prWaitExitChecksFailed ||
		!strings.Contains(plain.String(), "check:CI https://example.test/ci") {
		t.Fatalf("plain report = %q, code=%d", plain.String(), code)
	}
	var encoded bytes.Buffer
	if code := reportPROutcome(wait, prWaitOptions{JSON: true}, &encoded); code != prWaitExitChecksFailed {
		t.Fatalf("json code = %d", code)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded.Bytes(), &payload); err != nil || payload["outcome"] != "checks_failed" {
		t.Fatalf("json report = %q, err=%v", encoded.String(), err)
	}
}

func TestDescribePROutcomeDistinguishesUnresolvedThreads(t *testing.T) {
	result := &prReadiness{
		HeadSHA: "abcdef1234567890", Reviewer: "reviewer", ReviewState: prreadiness.ReviewUnresolved,
	}
	if got := describePROutcome(result, outcomeChangesRequested, prWaitOptions{}); got != "unresolved review threads block abcdef123456" {
		t.Fatalf("description = %q", got)
	}
}

func TestPRWaitCursorRoundTripsCanonicalState(t *testing.T) {
	dir := t.TempDir()
	opts := prWaitOptions{Host: "github.com", Owner: "SolenesInc", Name: "attn", Number: 303}
	saved := prWaitCursor{Cursor: prreadiness.Cursor{
		Initialized: true, Reviewer: "reviewer", HeadSHA: "head",
		SignalBaselineIDs: []string{"signal"}, SeenCommentIDs: []string{"comment"},
		DeliveredFeedbackIDs: []string{"feedback"}, SeenVerdictIDs: []string{"verdict"}, LastActionKey: "action",
	}}
	if err := savePRWaitCursor(dir, opts, saved, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPRWaitCursor(dir, opts)
	if err != nil || loaded.HeadSHA != "head" || loaded.LastActionKey != "action" ||
		strings.Join(loaded.SeenCommentIDs, ",") != "comment" ||
		strings.Join(loaded.DeliveredFeedbackIDs, ",") != "feedback" {
		t.Fatalf("loaded = %+v, err=%v", loaded, err)
	}
	data, err := os.ReadFile(cursorPath(dir, opts))
	if err != nil {
		t.Fatal(err)
	}
	for _, obsolete := range []string{"comment_ids", "verdict_at", "reaction_head", "failure_head"} {
		if bytes.Contains(data, []byte(`"`+obsolete+`"`)) {
			t.Fatalf("cursor retained obsolete field %q: %s", obsolete, data)
		}
	}
}

func TestPRWaitCursorWithoutDirectoryIsInert(t *testing.T) {
	if err := savePRWaitCursor("", prWaitOptions{Number: 1}, prWaitCursor{}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if loaded, err := loadPRWaitCursor("", prWaitOptions{Number: 1}); err != nil || loaded.Initialized {
		t.Fatalf("loaded = %+v, err=%v", loaded, err)
	}
}

func TestParsePRWaitArgs(t *testing.T) {
	opts, err := parsePRWaitArgs([]string{
		"https://github.com/SolenesInc/attn/pull/303", "--reviewer", "chatgpt-codex-connector[bot]",
		"--timeout", "2m", "--interval", "3s", "--since", "2026-09-20T10:00:00Z", "--json",
	})
	if err != nil || opts.Owner != "solenesinc" || opts.Name != "attn" || opts.Number != 303 ||
		opts.Timeout != 2*time.Minute || opts.Interval != 3*time.Second || !opts.JSON || opts.Since.IsZero() {
		t.Fatalf("opts = %+v, err=%v", opts, err)
	}
	if _, err := parsePRWaitArgs([]string{"303", "--reviewer", "reviewer"}); err == nil {
		t.Fatal("number without --repo succeeded")
	}
}

func TestResolvePRSelfLoginIsSkippableAndFailureTolerant(t *testing.T) {
	original := ghSelfLogin
	t.Cleanup(func() { ghSelfLogin = original })
	ghSelfLogin = func(context.Context, string) (string, error) { return "", errors.New("offline") }
	var stderr bytes.Buffer
	if got := resolvePRSelfLogin(context.Background(), prWaitOptions{}, &stderr); got != "" ||
		!strings.Contains(stderr.String(), "own comments will be reported") {
		t.Fatalf("login = %q, stderr=%q", got, stderr.String())
	}
	stderr.Reset()
	if got := resolvePRSelfLogin(context.Background(), prWaitOptions{IncludeSelf: true}, &stderr); got != "" || stderr.Len() != 0 {
		t.Fatalf("include-self login = %q, stderr=%q", got, stderr.String())
	}
}

func TestExecutePRCommandShowsSubcommandHelp(t *testing.T) {
	var output bytes.Buffer
	if code := executePRCommand([]string{"wait-ready", "--help"}, &output, io.Discard); code != 0 ||
		!strings.Contains(output.String(), "attn pr wait-ready") {
		t.Fatalf("help = %q, code=%d", output.String(), code)
	}
}

func TestCursorPathUsesRepositoryIdentity(t *testing.T) {
	path := cursorPath("/tmp/state", prWaitOptions{Host: "ghe.example", Owner: "o", Name: "r", Number: 7})
	if path != filepath.Join("/tmp/state", "ghe.example", "o", "r", "7.json") {
		t.Fatalf("path = %q", path)
	}
}
