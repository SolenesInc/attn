package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
	"github.com/victorarias/attn/internal/store"
)

type fakePRHost struct {
	snapshot       *github.PullRequestSnapshot
	review         string
	err            error
	reviewErr      error
	limited        bool
	limitReset     time.Time
	snapshots      int
	reviews        int
	readiness      *prreadiness.Observation
	feedback       []prreadiness.FeedbackItem
	threads        []prreadiness.ThreadState
	readyErr       error
	feedbackErr    error
	readinessCalls int
	feedbackCalls  int
}

func (f *fakePRHost) FetchPullRequestReadiness(context.Context, string, int) (*prreadiness.Observation, error) {
	f.readinessCalls++
	if f.readyErr != nil {
		return nil, f.readyErr
	}
	copy := *f.readiness
	return &copy, nil
}

func (f *fakePRHost) FetchPullRequestFeedback(context.Context, string, int) ([]prreadiness.FeedbackItem, []prreadiness.ThreadState, error) {
	f.feedbackCalls++
	return append([]prreadiness.FeedbackItem(nil), f.feedback...), append([]prreadiness.ThreadState(nil), f.threads...), f.feedbackErr
}

func (f *fakePRHost) FetchPullRequestSnapshot(string, int) (*github.PullRequestSnapshot, error) {
	f.snapshots++
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshot, nil
}

func (f *fakePRHost) FetchPullRequestReviewStatus(string, int) (string, error) {
	f.reviews++
	if f.reviewErr != nil {
		return "", f.reviewErr
	}
	return f.review, nil
}

func (f *fakePRHost) IsRateLimited(string) (bool, time.Time) { return f.limited, f.limitReset }

func (f *fakePRHost) GetRateLimit(string) *github.RateLimitInfo {
	if f.limitReset.IsZero() {
		return nil
	}
	return &github.RateLimitInfo{Resource: "core", ResetAt: f.limitReset}
}

func openSnapshot(title, mergeableState, headSHA string) *github.PullRequestSnapshot {
	return &github.PullRequestSnapshot{
		Number: 71, State: "open", Title: title,
		MergeableState: mergeableState, HeadSHA: headSHA, HeadRef: "pr-status-refresh",
	}
}

func serveHost(d *Daemon, host string, served *fakePRHost) {
	d.sessionPRHosts = func(name string) (sessionPRHost, bool) {
		if name != host {
			return nil, false
		}
		return served, true
	}
}

func recordPRForRefresh(t *testing.T, d *Daemon, sessionID, url string) {
	t.Helper()
	if resp := sendPRCommand(t, d, protocol.PullRequestCreatedMessage{
		Cmd: protocol.CmdPullRequestCreated, ID: sessionID, URL: url,
	}); !resp.Ok {
		t.Fatalf("record response = %+v", resp)
	}
}

func onlySessionPullRequest(t *testing.T, d *Daemon, sessionID string) protocol.SessionPullRequest {
	t.Helper()
	prs := sessionPullRequests(t, d, sessionID)
	if len(prs) != 1 {
		t.Fatalf("pull requests = %+v, want exactly one", prs)
	}
	return prs[0]
}

func storedPullRequest(t *testing.T, d *Daemon, sessionID string) store.SessionPullRequestRecord {
	t.Helper()
	records := d.store.ListSessionPullRequests(sessionID)
	if len(records) != 1 {
		t.Fatalf("records = %+v, want exactly one", records)
	}
	return records[0]
}

func TestSessionPullRequestRefreshLeavesTheGardenIdleWithoutOpenPullRequests(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	d.ensureGardenCollections()
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seeds collection: %v", err)
	}
	if _, err := d.store.PutDocument(*schema, "s-broken", []byte("[]"), time.Now(), nil); err != nil {
		t.Fatalf("put unreadable seed: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	d.logger = logger

	if fetched, changed := d.refreshSessionPullRequests(time.Now()); fetched != 0 || changed != 0 {
		t.Fatalf("refresh = (%d fetched, %d changed), want no work", fetched, changed)
	}
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read daemon log: %v", err)
	}
	if strings.Contains(string(logBody), "unreadable body") {
		t.Fatalf("idle refresh read the Garden: %s", logBody)
	}
}

func TestSessionPullRequestRefreshDoesNotDecodeGardenForInactiveUnarmedPullRequest(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	d.ensureGardenCollections()
	schema, err := d.seedsCollection()
	if err != nil {
		t.Fatalf("seeds collection: %v", err)
	}
	if _, err := d.store.PutDocument(*schema, "s-broken", []byte("[]"), time.Now(), nil); err != nil {
		t.Fatalf("put unreadable seed: %v", err)
	}
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	if closed, err := d.store.CloseSession("s1", store.SessionClose{}, time.Now()); err != nil || !closed {
		t.Fatalf("close session = %t, %v", closed, err)
	}
	host := &fakePRHost{snapshot: openSnapshot("Unarmed", "clean", "sha-1"), review: "none"}
	serveHost(d, "github.com", host)
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logger, err := logging.New(logPath)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	d.logger = logger

	if fetched, changed := d.refreshSessionPullRequests(time.Now()); fetched != 0 || changed != 0 {
		t.Fatalf("refresh = (%d fetched, %d changed), want no work", fetched, changed)
	}
	if host.snapshots != 0 {
		t.Fatalf("snapshot calls = %d, want the inactive unarmed PR left alone", host.snapshots)
	}
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read daemon log: %v", err)
	}
	if strings.Contains(string(logBody), "unreadable body") {
		t.Fatalf("inactive unarmed refresh decoded the Garden: %s", logBody)
	}
}

func TestSessionPullRequestRefreshFallsBackToActiveSessionsWhenGardenLookupFails(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	recordPRForRefresh(t, d, "s2", "https://github.com/victorarias/attn/pull/72")
	if closed, err := d.store.CloseSession("s2", store.SessionClose{}, time.Now()); err != nil || !closed {
		t.Fatalf("close session = %t, %v", closed, err)
	}
	if _, err := d.store.DeleteDocumentCollection(garden.Namespace, garden.CollectionSeeds); err != nil {
		t.Fatalf("remove Garden collection: %v", err)
	}
	host := &fakePRHost{snapshot: openSnapshot("Active only", "clean", "sha-1"), review: "none"}
	serveHost(d, "github.com", host)

	if fetched, changed := d.refreshSessionPullRequests(time.Now()); fetched != 1 || changed != 1 {
		t.Fatalf("refresh = (%d fetched, %d changed), want only the active session", fetched, changed)
	}
	if host.snapshots != 1 {
		t.Fatalf("snapshot calls = %d, want only the active session's PR", host.snapshots)
	}
}

func TestSessionPullRequestRefreshTracksGitHub(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{snapshot: openSnapshot("Keep session PR status fresh", "blocked", "sha-1"), review: "approved"}
	serveHost(d, "github.com", host)

	now := time.Now()
	if fetched, changed := d.refreshSessionPullRequests(now); fetched != 1 || changed != 1 {
		t.Fatalf("refresh = (%d fetched, %d changed), want (1, 1)", fetched, changed)
	}

	entry := onlySessionPullRequest(t, d, "s1")
	if protocol.Deref(entry.Title) != "Keep session PR status fresh" {
		t.Errorf("title = %v, want the fetched one", entry.Title)
	}
	if entry.State != "open" {
		t.Errorf("state = %q, want open", entry.State)
	}
	if protocol.Deref(entry.CIStatus) != "pending" {
		t.Errorf("ci status = %v, want pending for mergeable_state blocked", entry.CIStatus)
	}
	if protocol.Deref(entry.ReviewStatus) != "approved" {
		t.Errorf("review status = %v, want approved", entry.ReviewStatus)
	}
	if protocol.Deref(entry.MergeableState) != "blocked" {
		t.Errorf("mergeable state = %v, want blocked", entry.MergeableState)
	}
	if entry.StatusFetchedAt == nil {
		t.Error("status_fetched_at is unset, want the time of the fetch")
	}
	if rec := storedPullRequest(t, d, "s1"); rec.HeadSHA != "sha-1" || rec.HeadBranch != "pr-status-refresh" {
		t.Errorf("head = %s on %s, want sha-1 on pr-status-refresh", rec.HeadSHA, rec.HeadBranch)
	}
	if host.snapshots != 1 || host.reviews != 1 {
		t.Errorf("calls = %d snapshots and %d review reads, want one of each", host.snapshots, host.reviews)
	}
}

func TestSessionPullRequestRefreshPublishesOnlyRealChanges(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{snapshot: openSnapshot("Fresh status", "clean", "sha-1"), review: "none"}
	serveHost(d, "github.com", host)

	cap := captureBroadcasts(d)
	now := time.Now()
	d.refreshSessionPullRequests(now)
	if events := sessionUpdates(cap, "s1"); len(events) != 1 {
		t.Fatalf("session updates after the first fetch = %d, want one", len(events))
	}

	if _, changed := d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval)); changed != 0 {
		t.Fatalf("changed = %d on an unchanged pull request, want 0", changed)
	}
	if events := sessionUpdates(cap, "s1"); len(events) != 1 {
		t.Fatalf("session updates = %d, want no second one", len(events))
	}

	host.snapshot = openSnapshot("Fresh status", "dirty", "sha-2")
	if _, changed := d.refreshSessionPullRequests(now.Add(2 * protocol.HeatHotInterval)); changed != 1 {
		t.Fatalf("changed = %d after CI turned red, want 1", changed)
	}
	if events := sessionUpdates(cap, "s1"); len(events) != 2 {
		t.Fatalf("session updates = %d, want a second one", len(events))
	}
	if entry := onlySessionPullRequest(t, d, "s1"); protocol.Deref(entry.CIStatus) != "failure" {
		t.Errorf("ci status = %v, want failure", entry.CIStatus)
	}
}

func TestSessionPullRequestRefreshPacesByHeat(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{snapshot: openSnapshot("Fresh status", "clean", "sha-1"), review: "none"}
	serveHost(d, "github.com", host)

	tests := []struct {
		name        string
		sinceActive time.Duration
		tooSoon     time.Duration
		due         time.Duration
	}{
		{"hot", 0, protocol.HeatHotInterval - time.Second, protocol.HeatHotInterval},
		{"warm", protocol.HeatHotDuration, protocol.HeatWarmInterval - time.Second, protocol.HeatWarmInterval},
		{"cold", protocol.HeatWarmDuration, protocol.HeatColdInterval - time.Second, protocol.HeatColdInterval},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			active := time.Now()
			if err := d.store.TouchSessionPullRequestActivity("github.com:victorarias/attn#71", active); err != nil {
				t.Fatalf("touch activity: %v", err)
			}
			fetchedAt := active.Add(tc.sinceActive)
			if err := d.store.MarkSessionPullRequestChecked("github.com:victorarias/attn#71", fetchedAt); err != nil {
				t.Fatalf("mark checked: %v", err)
			}

			before := host.snapshots
			if fetched, _ := d.refreshSessionPullRequests(fetchedAt.Add(tc.tooSoon)); fetched != 0 {
				t.Fatalf("fetched %d at %s after the last look, want none before %s", fetched, tc.tooSoon, tc.due)
			}
			if fetched, _ := d.refreshSessionPullRequests(fetchedAt.Add(tc.due)); fetched != 1 {
				t.Fatalf("fetched %d at %s after the last look, want one", fetched, tc.due)
			}
			if host.snapshots != before+1 {
				t.Fatalf("snapshot calls = %d, want exactly one more than %d", host.snapshots, before)
			}
		})
	}
}

func TestSessionPullRequestRefreshStopsAfterMerge(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{snapshot: openSnapshot("Fresh status", "clean", "sha-1"), review: "approved"}
	serveHost(d, "github.com", host)

	now := time.Now()
	d.refreshSessionPullRequests(now)

	host.snapshot = &github.PullRequestSnapshot{
		Number: 71, State: "closed", Merged: true, Title: "Fresh status",
		MergeableState: "unknown", HeadSHA: "sha-1", HeadRef: "pr-status-refresh",
	}
	merged := now.Add(protocol.HeatHotInterval)
	if _, changed := d.refreshSessionPullRequests(merged); changed != 1 {
		t.Fatal("the merge did not register as a change")
	}
	entry := onlySessionPullRequest(t, d, "s1")
	if entry.State != "merged" {
		t.Errorf("state = %q, want merged", entry.State)
	}
	if protocol.Deref(entry.CIStatus) != "success" {
		t.Errorf("ci status = %v, want the result it merged with", entry.CIStatus)
	}
	if protocol.Deref(entry.ReviewStatus) != "approved" {
		t.Errorf("review status = %v, want the review it merged with", entry.ReviewStatus)
	}

	before := host.snapshots
	if fetched, _ := d.refreshSessionPullRequests(merged.Add(2 * protocol.HeatColdInterval)); fetched != 0 {
		t.Fatalf("fetched %d after the merge, want a finished pull request left alone", fetched)
	}
	if host.snapshots != before {
		t.Errorf("snapshot calls = %d, want no more after the merge", host.snapshots)
	}
}

func TestSessionPullRequestRefreshLeavesHostsItCannotReach(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://ghe.example.test/acme/widget/pull/12")
	serveHost(d, "github.com", &fakePRHost{snapshot: openSnapshot("unrelated", "clean", "sha-1")})

	now := time.Now()
	if fetched, changed := d.refreshSessionPullRequests(now); fetched != 0 || changed != 0 {
		t.Fatalf("refresh = (%d, %d), want nothing fetched for a host attn has no client for", fetched, changed)
	}
	entry := onlySessionPullRequest(t, d, "s1")
	if entry.State != "open" || entry.CIStatus != nil || entry.StatusFetchedAt != nil {
		t.Errorf("entry = %+v, want the row as recorded", entry)
	}
	rec := storedPullRequest(t, d, "s1")
	if rec.StatusCheckedAt == "" {
		t.Error("status_checked_at is unset, want the attempt recorded")
	}
	if rec.StatusFetchedAt != "" {
		t.Errorf("status_fetched_at = %q, want it empty until a status lands", rec.StatusFetchedAt)
	}
}

func TestSessionPullRequestRefreshSkipsRateLimitedHosts(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	resetAt := time.Now().Add(10 * time.Minute)
	host := &fakePRHost{
		snapshot: openSnapshot("Fresh status", "clean", "sha-1"), review: "none",
		limited: true, limitReset: resetAt,
	}
	serveHost(d, "github.com", host)

	if fetched, _ := d.refreshSessionPullRequests(time.Now()); fetched != 0 {
		t.Fatalf("fetched %d while rate limited, want none", fetched)
	}
	if host.snapshots != 0 {
		t.Errorf("snapshot calls = %d, want none while rate limited", host.snapshots)
	}
	if rec := storedPullRequest(t, d, "s1"); rec.StatusCheckedAt != "" {
		t.Errorf("status_checked_at = %q, want it untouched by a rate limit", rec.StatusCheckedAt)
	}

	host.limited = false
	if fetched, _ := d.refreshSessionPullRequests(time.Now()); fetched != 1 {
		t.Fatal("the row was not fetched once the limit lifted")
	}
}

func TestSessionPullRequestRefreshReportsFetchFailuresWithoutClaimingAStatus(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{err: fmt.Errorf("fetch pull request snapshot: 404 Not Found")}
	serveHost(d, "github.com", host)

	now := time.Now()
	if fetched, _ := d.refreshSessionPullRequests(now); fetched != 0 {
		t.Fatal("a failed fetch counted as a fetch")
	}
	rec := storedPullRequest(t, d, "s1")
	if rec.StatusFetchedAt != "" {
		t.Errorf("status_fetched_at = %q, want it empty after a failure", rec.StatusFetchedAt)
	}
	if rec.StatusCheckedAt == "" {
		t.Error("status_checked_at is unset, want the failed attempt to pace the next one")
	}
	if fetched, _ := d.refreshSessionPullRequests(now.Add(time.Second)); fetched != 0 {
		t.Fatal("a failing pull request was retried before its interval elapsed")
	}
	if host.snapshots != 1 {
		t.Errorf("snapshot calls = %d, want the failure to have backed off", host.snapshots)
	}
}

func TestSessionPullRequestRefreshKeepsTheReviewItHasWhenReviewsFail(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{snapshot: openSnapshot("Fresh status", "clean", "sha-1"), review: "approved"}
	serveHost(d, "github.com", host)

	now := time.Now()
	d.refreshSessionPullRequests(now)

	host.reviewErr = fmt.Errorf("fetch PR reviews: 500 Internal Server Error")
	host.snapshot = openSnapshot("Fresh status", "dirty", "sha-2")
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))

	entry := onlySessionPullRequest(t, d, "s1")
	if protocol.Deref(entry.CIStatus) != "failure" {
		t.Errorf("ci status = %v, want the snapshot half to have landed", entry.CIStatus)
	}
	if protocol.Deref(entry.ReviewStatus) != "approved" {
		t.Errorf("review status = %v, want the last one that landed", entry.ReviewStatus)
	}
}

func TestSessionPullRequestRefreshSkipsSessionsWithNoRuntime(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{snapshot: openSnapshot("Fresh status", "clean", "sha-1"), review: "none"}
	serveHost(d, "github.com", host)

	if !d.store.UpdateState("s1", string(protocol.SessionStateRecoverable)) {
		t.Fatal("could not put the session in the recoverable state")
	}
	if fetched, _ := d.refreshSessionPullRequests(time.Now()); fetched != 0 {
		t.Fatalf("fetched %d for a session with no runtime, want none", fetched)
	}

	if !d.store.UpdateState("s1", string(protocol.SessionStateIdle)) {
		t.Fatal("could not reload the session")
	}
	if fetched, _ := d.refreshSessionPullRequests(time.Now()); fetched != 1 {
		t.Fatal("the reloaded session's pull request was not picked up again")
	}
}

func TestSessionPullRequestRefreshKeepsWatchingRecoverableSessions(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", prreadiness.ModeGreen, "")
	host := &fakePRHost{readiness: readinessObservation()}
	serveHost(d, "github.com", host)
	if !d.store.UpdateState("s1", string(protocol.SessionStateRecoverable)) {
		t.Fatal("could not put the session in the recoverable state")
	}

	if fetched, _ := d.refreshSessionPullRequests(time.Now()); fetched != 1 {
		t.Fatalf("fetched = %d, want the durable watch to remain active", fetched)
	}
	if host.readinessCalls != 1 {
		t.Fatalf("readiness calls = %d, want one", host.readinessCalls)
	}
}

func TestSessionPullRequestRefreshFetchesOncePerPullRequest(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	url := "https://github.com/victorarias/attn/pull/71"
	recordPRForRefresh(t, d, "s1", url)
	recordPRForRefresh(t, d, "s2", url)
	host := &fakePRHost{snapshot: openSnapshot("Fresh status", "clean", "sha-1"), review: "none"}
	serveHost(d, "github.com", host)

	cap := captureBroadcasts(d)
	if fetched, _ := d.refreshSessionPullRequests(time.Now()); fetched != 1 {
		t.Fatalf("fetched %d for one pull request in two sessions, want one", fetched)
	}
	if host.snapshots != 1 {
		t.Errorf("snapshot calls = %d, want one shared fetch", host.snapshots)
	}
	for _, sessionID := range []string{"s1", "s2"} {
		if entry := onlySessionPullRequest(t, d, sessionID); protocol.Deref(entry.CIStatus) != "success" {
			t.Errorf("%s ci status = %v, want the shared result", sessionID, entry.CIStatus)
		}
		if events := sessionUpdates(cap, sessionID); len(events) != 1 {
			t.Errorf("%s session updates = %d, want one", sessionID, len(events))
		}
	}
}

func TestSessionPullRequestReheatsWhenTheInboxSeesTheSamePullRequest(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{snapshot: openSnapshot("Fresh status", "clean", "sha-1"), review: "none"}
	serveHost(d, "github.com", host)

	now := time.Now()
	d.refreshSessionPullRequests(now)
	stale := now.Add(-2 * protocol.HeatWarmDuration)
	if err := d.store.TouchSessionPullRequestActivity("github.com:victorarias/attn#71", stale); err != nil {
		t.Fatalf("age the row: %v", err)
	}
	if fetched, _ := d.refreshSessionPullRequests(now.Add(protocol.HeatWarmInterval)); fetched != 0 {
		t.Fatal("a cold row was refreshed at the warm interval")
	}

	d.reheatSessionPullRequest(bus.Event{Name: FactPRUpdated, Subject: "github.com:victorarias/attn#71"})
	if fetched, _ := d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval)); fetched != 1 {
		t.Fatal("the inbox's report did not put the row back on the hot cadence")
	}
}

func readinessObservation() *prreadiness.Observation {
	return &prreadiness.Observation{
		Number: 71, URL: "https://github.com/victorarias/attn/pull/71", Title: "Ready to ship",
		State: "open", HeadSHA: "head-a", HeadRef: "pr-readiness", MergeStateStatus: "CLEAN",
	}
}

func watchPRForRefresh(t *testing.T, d *Daemon, sessionID string, mode prreadiness.Mode, reviewer string) {
	t.Helper()
	rec, err := d.sessionPullRequestIdentity(sessionID, "https://github.com/victorarias/attn/pull/71")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.watchSessionPullRequest(rec, mode, reviewer); err != nil {
		t.Fatal(err)
	}
}

func TestWatchedPullRequestSharesOneObservationAcrossConsumerModes(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	watchPRForRefresh(t, d, "s1", prreadiness.ModeGreen, "")
	watchPRForRefresh(t, d, "s2", prreadiness.ModeFormalReview, "victor")
	observation := readinessObservation()
	observation.ReviewOpinions = []prreadiness.ReviewOpinion{{Actor: "victor", State: "APPROVED"}}
	observation.ReviewDecision = "REVIEW_REQUIRED"
	host := &fakePRHost{readiness: observation}
	serveHost(d, "github.com", host)

	if fetched, _ := d.refreshSessionPullRequests(time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC)); fetched != 1 {
		t.Fatalf("fetched = %d, want one shared acquisition", fetched)
	}
	if host.readinessCalls != 1 || host.feedbackCalls != 1 || host.snapshots != 0 || host.reviews != 0 {
		t.Fatalf("calls = readiness:%d feedback:%d snapshots:%d reviews:%d", host.readinessCalls, host.feedbackCalls, host.snapshots, host.reviews)
	}
	for _, test := range []struct {
		session string
		mode    protocol.PullRequestWatchMode
	}{
		{"s1", protocol.PullRequestWatchModeGreen},
		{"s2", protocol.PullRequestWatchModeFormalReview},
	} {
		entry := onlySessionPullRequest(t, d, test.session)
		if !protocol.Deref(entry.Watching) || protocol.Deref(entry.WatchMode) != test.mode || protocol.Deref(entry.ReadinessState) != prreadiness.StateReady {
			t.Errorf("%s projection = %+v", test.session, entry)
		}
		if protocol.Deref(entry.MergeableState) != "clean" || protocol.Deref(entry.ReviewStatus) != "pending" {
			t.Errorf("%s normalized status = merge:%v review:%v", test.session, entry.MergeableState, entry.ReviewStatus)
		}
		deliveries, err := d.store.UnreadAgentMailboxDeliveries(test.session)
		if err != nil || len(deliveries) != 1 || !strings.Contains(deliveries[0].Item.Prompt, "ready") {
			t.Errorf("%s mailbox = %+v, %v", test.session, deliveries, err)
		}
	}
}

func TestCodexWatchSchedulesAndHonorsItsSettlingDeadline(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	runner := jobs.New(jobs.Options{Store: newTestJobStore(t, d), Now: func() time.Time { return now }})
	if err := runner.RegisterWith(sessionPullRequestSettleKind, d.sessionPullRequestSettleHandler, jobs.HandlerConfig{}); err != nil {
		t.Fatal(err)
	}
	d.setJobQueue(runner)
	t.Cleanup(func() { d.setJobQueue(nil) })
	watchPRForRefresh(t, d, "s1", prreadiness.ModeCodex, "")
	observation := readinessObservation()
	observation.CodexThumbsUp = true
	host := &fakePRHost{readiness: observation}
	serveHost(d, "github.com", host)

	d.refreshSessionPullRequestsContext(context.Background(), now, "github.com:victorarias/attn#71")
	entry := onlySessionPullRequest(t, d, "s1")
	if protocol.Deref(entry.ReadinessState) != prreadiness.StateWaiting || protocol.Deref(entry.ReadinessReason) != "codex_settling" {
		t.Fatalf("settling projection = %+v", entry)
	}
	job, err := runner.GetByKey(sessionPullRequestSettleKind, "github.com:victorarias/attn#71")
	if err != nil || job == nil || !job.ScheduledAt.Equal(now.Add(prreadiness.CodexSettle)) {
		t.Fatalf("settle job = %+v, %v", job, err)
	}

	d.refreshSessionPullRequestsContext(context.Background(), now.Add(prreadiness.CodexSettle), "github.com:victorarias/attn#71")
	if entry = onlySessionPullRequest(t, d, "s1"); protocol.Deref(entry.ReadinessState) != prreadiness.StateReady {
		t.Fatalf("deadline projection = %+v", entry)
	}
}

func TestWatchedPullRequestOutageCoalescesAndRecoveryIsSilent(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", prreadiness.ModeGreen, "")
	host := &fakePRHost{readyErr: fmt.Errorf("GitHub unavailable")}
	serveHost(d, "github.com", host)
	base := time.Date(2026, 9, 21, 13, 0, 0, 0, time.UTC)
	prID := "github.com:victorarias/attn#71"

	d.refreshSessionPullRequestsContext(context.Background(), base, prID)
	d.refreshSessionPullRequestsContext(context.Background(), base.Add(time.Second), prID)
	deliveries, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 1 || !strings.Contains(deliveries[0].Item.Prompt, "monitoring is delayed") {
		t.Fatalf("outage mailbox = %+v, %v", deliveries, err)
	}
	if _, _, err := d.store.ReadAgentMailbox("s1", 10, base.Add(1500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	host.readyErr = nil
	host.readiness = readinessObservation()
	d.refreshSessionPullRequestsContext(context.Background(), base.Add(2*time.Second), prID)
	deliveries, err = d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 1 || !strings.Contains(deliveries[0].Item.Prompt, "ready") {
		t.Fatalf("recovered mailbox = %+v, %v", deliveries, err)
	}
	entry := onlySessionPullRequest(t, d, "s1")
	if protocol.Deref(entry.WatchHealth) != "current" || entry.WatchError != nil {
		t.Fatalf("recovered projection = %+v", entry)
	}
	if _, _, err := d.store.ReadAgentMailbox("s1", 10, base.Add(2500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	host.readyErr = fmt.Errorf("GitHub unavailable again")
	d.refreshSessionPullRequestsContext(context.Background(), base.Add(3*time.Second), prID)
	deliveries, err = d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 1 || !strings.Contains(deliveries[0].Item.Prompt, "monitoring is delayed") {
		t.Fatalf("recurring outage mailbox = %+v, %v", deliveries, err)
	}
}

func TestFeedbackFailureDoesNotInvalidateReadyState(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", prreadiness.ModeGreen, "")
	host := &fakePRHost{readiness: readinessObservation(), feedbackErr: fmt.Errorf("review threads unavailable")}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(time.Date(2026, 9, 21, 14, 0, 0, 0, time.UTC))

	entry := onlySessionPullRequest(t, d, "s1")
	if protocol.Deref(entry.ReadinessState) != prreadiness.StateReady || protocol.Deref(entry.WatchHealth) != "delayed" || !strings.Contains(protocol.Deref(entry.WatchError), "feedback") {
		t.Fatalf("projection = %+v", entry)
	}
}

func TestTerminalObservationNotifiesThenStopsTheWatch(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", prreadiness.ModeGreen, "")
	observation := readinessObservation()
	observation.State = "closed"
	observation.Merged = true
	host := &fakePRHost{readiness: observation}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC))

	entry := onlySessionPullRequest(t, d, "s1")
	if protocol.Deref(entry.Watching) || entry.State != "merged" || protocol.Deref(entry.ReadinessState) != prreadiness.StateMerged {
		t.Fatalf("terminal projection = %+v", entry)
	}
	if _, ok := d.store.PullRequestWatch("s1", "github.com:victorarias/attn#71"); ok {
		t.Fatal("terminal watch still exists")
	}
	deliveries, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 1 || !strings.Contains(deliveries[0].Item.Prompt, "merged") {
		t.Fatalf("terminal mailbox = %+v, %v", deliveries, err)
	}
}
