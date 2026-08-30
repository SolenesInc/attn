package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type fakePRHost struct {
	snapshot   *github.PullRequestSnapshot
	review     string
	err        error
	reviewErr  error
	limited    bool
	limitReset time.Time
	snapshots  int
	reviews    int
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
