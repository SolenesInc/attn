package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/logging"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
	"github.com/victorarias/attn/internal/store"
)

type fakePRHost struct {
	snapshot   *github.PullRequestSnapshot
	readiness  *github.PullRequestReadiness
	review     string
	err        error
	readyErr   error
	reviewErr  error
	limited    bool
	limitReset time.Time
	snapshots  int
	reviews    int
	readyReads int
	limitedFor string
	limitFor   string
	onFetch    func()
}

func (f *fakePRHost) FetchPullRequestReadiness(string, int) (*github.PullRequestReadiness, error) {
	f.readyReads++
	if f.onFetch != nil {
		f.onFetch()
	}
	if f.readyErr != nil {
		return nil, f.readyErr
	}
	return f.readiness, nil
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

func (f *fakePRHost) IsRateLimited(resource string) (bool, time.Time) {
	f.limitedFor = resource
	return f.limited, f.limitReset
}

func (f *fakePRHost) GetRateLimit(resource string) *github.RateLimitInfo {
	f.limitFor = resource
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

func watchedReadiness(head, checks, review string) *github.PullRequestReadiness {
	snapshot := openSnapshot("Watched pull request", "clean", head)
	evidence := prreadiness.Evidence{
		State: "open", MergeableState: "clean", HeadSHA: head,
		HeadObservedAt: time.Now().Add(-time.Minute), CheckState: checks,
	}
	if review != "" {
		evidence.Reviews = []prreadiness.Review{{
			ID: "review-" + head, Author: "chatgpt-codex-connector", State: review,
			CommitOID: head, SubmittedAt: time.Now(),
		}}
	}
	return &github.PullRequestReadiness{Snapshot: snapshot, Evidence: evidence}
}

func watchPRForRefresh(t *testing.T, d *Daemon, sessionID, url string) {
	t.Helper()
	if resp := sendPRCommand(t, d, protocol.PullRequestWatchMessage{
		Cmd: protocol.CmdPullRequestWatch, ID: sessionID, URL: url,
		Reviewer: "chatgpt-codex-connector[bot]",
	}); !resp.Ok {
		t.Fatalf("watch response = %+v", resp)
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

func TestPullRequestWatchIsDurableIdempotentAndVisible(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	watchPRForRefresh(t, d, "s1", url)

	watches := d.store.PullRequestWatches()
	if len(watches) != 1 || watches[0].Reviewer != "chatgpt-codex-connector[bot]" {
		t.Fatalf("watches = %+v", watches)
	}
	entry := onlySessionPullRequest(t, d, "s1")
	if !protocol.Deref(entry.Watching) || len(entry.WatchRecipients) != 1 || entry.WatchRecipients[0] != "workspace-s1" {
		t.Fatalf("broadcast watch = %+v", entry)
	}
}

func TestPullRequestWatchRevisitsClosedRecordUntilDisarmed(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	closed := watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
	closed.Snapshot.State = "closed"
	closed.Snapshot.Merged = true
	host := &fakePRHost{readiness: closed}
	serveHost(d, "github.com", host)

	d.refreshSessionPullRequests(time.Now())
	watchPRForRefresh(t, d, "s1", url)
	if fetched, _ := d.refreshSessionPullRequests(time.Now().Add(protocol.HeatHotInterval)); fetched != 1 {
		t.Fatalf("closed armed watch fetched %d times, want one cleanup retry", fetched)
	}
	if watches := d.store.PullRequestWatches(); len(watches) != 0 {
		t.Fatalf("closed watch remains armed: %+v", watches)
	}
}

func TestPullRequestWatchReviewerChangeClearsUnreadResult(t *testing.T) {
	d := newPersistentPRDaemonForTest(t)
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	host := &fakePRHost{readiness: watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(time.Now())
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 {
		t.Fatalf("initial unread result = %+v, %v", unread, err)
	}
	direct, err := store.OpenDB(d.store.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	if _, err := direct.Exec(`CREATE TRIGGER reject_cleanup BEFORE DELETE ON agent_mailbox_items
		BEGIN SELECT RAISE(FAIL, 'injected cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	if resp := sendPRCommand(t, d, protocol.PullRequestWatchMessage{
		Cmd: protocol.CmdPullRequestWatch, ID: "s1", URL: url, Reviewer: "alternate-reviewer",
	}); resp.Ok {
		t.Fatalf("failed cleanup reported success: %+v", resp)
	}
	if watch := d.store.PullRequestWatches()[0]; watch.Reviewer != "chatgpt-codex-connector[bot]" {
		t.Fatalf("failed cleanup committed the new reviewer: %+v", watch)
	}
	if _, err := direct.Exec("DROP TRIGGER reject_cleanup"); err != nil {
		t.Fatal(err)
	}

	if resp := sendPRCommand(t, d, protocol.PullRequestWatchMessage{
		Cmd: protocol.CmdPullRequestWatch, ID: "s1", URL: url, Reviewer: "alternate-reviewer",
	}); !resp.Ok {
		t.Fatalf("reviewer change response = %+v", resp)
	}
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("stale reviewer result remains unread: %+v, %v", unread, err)
	}
	if entry := onlySessionPullRequest(t, d, "s1"); protocol.Deref(entry.ReviewStatus) != prreadiness.ReviewWaiting {
		t.Fatalf("old reviewer verdict survived reviewer change: %+v", entry)
	}
	host.readyErr = errors.New("review API unavailable")
	d.refreshSessionPullRequests(time.Now().Add(protocol.HeatHotInterval))
	if entry := onlySessionPullRequest(t, d, "s1"); protocol.Deref(entry.ReviewStatus) != prreadiness.ReviewWaiting {
		t.Fatalf("failed fetch restored old reviewer verdict: %+v", entry)
	}
}

func TestPullRequestWatchSharesFetchAndKeepsHotCadence(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	watchPRForRefresh(t, d, "s2", url)
	for _, sessionID := range []string{"s1", "s2"} {
		if closed, err := d.store.CloseSession(sessionID, store.SessionClose{}, time.Now()); err != nil || !closed {
			t.Fatalf("close %s = %t, %v", sessionID, closed, err)
		}
	}
	host := &fakePRHost{readiness: watchedReadiness("sha-1", prreadiness.ChecksPending, "")}
	serveHost(d, "github.com", host)
	now := time.Now()
	if fetched, _ := d.refreshSessionPullRequests(now); fetched != 1 || host.readyReads != 1 {
		t.Fatalf("shared refresh = fetched %d reads %d", fetched, host.readyReads)
	}
	if fetched, _ := d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval - time.Second)); fetched != 0 {
		t.Fatalf("watch refreshed early: %d", fetched)
	}
	if fetched, _ := d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval)); fetched != 1 {
		t.Fatalf("watch did not stay hot: %d", fetched)
	}
}

func TestPullRequestWatchStoresEachReviewersStatus(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	if resp := sendPRCommand(t, d, protocol.PullRequestWatchMessage{
		Cmd: protocol.CmdPullRequestWatch, ID: "s2", URL: url, Reviewer: "alternate-reviewer",
	}); !resp.Ok {
		t.Fatalf("alternate watch response = %+v", resp)
	}
	readiness := watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
	readiness.Evidence.Reviews = append(readiness.Evidence.Reviews, prreadiness.Review{
		ID: "alternate", Author: "alternate-reviewer", State: "CHANGES_REQUESTED",
		CommitOID: "sha-1", SubmittedAt: time.Now(),
	})
	host := &fakePRHost{readiness: readiness}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(time.Now())

	if got := storedPullRequest(t, d, "s1").ReviewStatus; got != prreadiness.ReviewApproved {
		t.Fatalf("s1 review status = %q", got)
	}
	if got := storedPullRequest(t, d, "s2").ReviewStatus; got != prreadiness.ReviewChangesRequested {
		t.Fatalf("s2 review status = %q", got)
	}
}

func TestPullRequestWatchDoesNotShareReviewerStatusWithUnwatchedSession(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	url := "https://github.com/victorarias/attn/pull/71"
	recordPRForRefresh(t, d, "s2", url)
	watchPRForRefresh(t, d, "s1", url)
	host := &fakePRHost{readiness: watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")}
	serveHost(d, "github.com", host)
	now := time.Now()
	d.refreshSessionPullRequests(now)
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))

	if got := storedPullRequest(t, d, "s1").ReviewStatus; got != prreadiness.ReviewApproved {
		t.Fatalf("watched review status = %q", got)
	}
	if got := storedPullRequest(t, d, "s2").ReviewStatus; got != "" {
		t.Fatalf("unwatched review status = %q, want empty", got)
	}
}

func TestPullRequestUnwatchScopesOneRecipient(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	watchPRForRefresh(t, d, "s2", url)
	resp := sendPRCommand(t, d, protocol.PullRequestUnwatchMessage{
		Cmd: protocol.CmdPullRequestUnwatch, ID: "s1", URL: url,
	})
	if !resp.Ok {
		t.Fatalf("unwatch response = %+v", resp)
	}
	watches := d.store.PullRequestWatches()
	if len(watches) != 1 || watches[0].SessionID != "s2" {
		t.Fatalf("remaining watches = %+v", watches)
	}
}

func TestPullRequestWatchInvalidatesStaleReadyMessageOnNewHead(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	host := &fakePRHost{readiness: watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")}
	serveHost(d, "github.com", host)
	now := time.Now()
	d.refreshSessionPullRequests(now)
	deliveries, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 1 || !strings.Contains(deliveries[0].Item.Prompt, "ready") {
		t.Fatalf("ready delivery = %+v, %v", deliveries, err)
	}

	host.readiness = watchedReadiness("sha-2", prreadiness.ChecksPending, "")
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	deliveries, err = d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 0 {
		t.Fatalf("stale ready delivery survived head change: %+v, %v", deliveries, err)
	}
}

func TestPullRequestWatchClearsReadyWhenSameHeadBecomesPending(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	host := &fakePRHost{readiness: watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")}
	serveHost(d, "github.com", host)
	now := time.Now()
	d.refreshSessionPullRequests(now)

	host.readiness = watchedReadiness("sha-1", prreadiness.ChecksPending, "COMMENTED")
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("same-head stale ready survived pending checks: %+v, %v", unread, err)
	}
}

func TestPullRequestWatchReportsHumanFeedbackAfterReady(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	ready := watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
	feedbackAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	ready.Evidence.Comments = []prreadiness.Comment{{ID: "existing", Author: "reviewer", Body: "Existing feedback", CreatedAt: feedbackAt}}
	host := &fakePRHost{readiness: ready}
	serveHost(d, "github.com", host)
	now := time.Now()
	d.refreshSessionPullRequests(now)

	ready.Evidence.Comments = append(ready.Evidence.Comments, prreadiness.Comment{
		ID: "human", Author: "reviewer", Body: "Please check the retry path.", CreatedAt: feedbackAt,
	})
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 2 || !strings.Contains(unread[1].Item.Prompt, "Please check the retry path") {
		t.Fatalf("feedback delivery = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchRetainsUnreadFeedbackUntilRead(t *testing.T) {
	for _, checks := range []string{prreadiness.ChecksPending, prreadiness.ChecksGreen} {
		t.Run(checks, func(t *testing.T) {
			d := newPRDaemonForTest(t, "s1")
			watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
			now := time.Now()
			ready := watchedReadiness("sha-1", checks, "COMMENTED")
			host := &fakePRHost{readiness: ready}
			serveHost(d, "github.com", host)
			d.refreshSessionPullRequests(now.Add(-protocol.HeatHotInterval))
			ready.Evidence.Comments = []prreadiness.Comment{{
				ID: "first", Author: "human", Body: "Keep the retry guard.", CreatedAt: now.Add(time.Second),
			}}
			assertFeedback := func(want int) {
				t.Helper()
				unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
				if err != nil {
					t.Fatal(err)
				}
				first, second := 0, 0
				for _, delivery := range unread {
					first += strings.Count(delivery.Item.Prompt, "Keep the retry guard.")
					second += strings.Count(delivery.Item.Prompt, "Also keep the timeout guard.")
				}
				if first != 1 || second != want-1 {
					t.Fatalf("unread feedback first=%d second=%d, want 1 and %d: %+v", first, second, want-1, unread)
				}
			}
			d.refreshSessionPullRequests(now)
			assertFeedback(1)
			d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
			assertFeedback(1)
			ready.Evidence.Comments = append(ready.Evidence.Comments, prreadiness.Comment{
				ID: "second", Author: "human", Body: "Also keep the timeout guard.", CreatedAt: now.Add(time.Second),
			})
			d.refreshSessionPullRequests(now.Add(2 * protocol.HeatHotInterval))
			assertFeedback(2)
			host.readiness = watchedReadiness("sha-2", prreadiness.ChecksPending, "")
			host.readiness.Evidence.Comments = ready.Evidence.Comments
			d.refreshSessionPullRequests(now.Add(3 * protocol.HeatHotInterval))
			assertFeedback(2)
			if _, _, err := d.store.ReadAgentMailbox("s1", 20, now.Add(4*protocol.HeatHotInterval)); err != nil {
				t.Fatal(err)
			}
			d.refreshSessionPullRequests(now.Add(5 * protocol.HeatHotInterval))
			if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
				t.Fatalf("read feedback reappeared: %+v, %v", unread, err)
			}
		})
	}
}

func newPersistentPRDaemonForTest(t *testing.T) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	stopDaemonBackground(t, d)
	dbPath := filepath.Join(t.TempDir(), "watch.db")
	persistent, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.store.Close()
	d.store = persistent
	t.Cleanup(func() { _ = d.store.Close() })
	registerSessionForPRTest(t, d, "s1")
	return d
}

func TestPullRequestWatchRetriesFeedbackEnqueueAfterRestart(t *testing.T) {
	d := newPersistentPRDaemonForTest(t)
	dbPath := d.store.DatabasePath()
	watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	direct, err := store.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	if _, err := direct.Exec(`CREATE TRIGGER reject_second_feedback BEFORE INSERT ON agent_mailbox_items
		WHEN NEW.prompt LIKE '%second feedback%' BEGIN SELECT RAISE(FAIL, 'injected enqueue failure'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ready := watchedReadiness("sha-1", prreadiness.ChecksPending, "")
	serveHost(d, "github.com", &fakePRHost{readiness: ready})
	d.refreshSessionPullRequests(now.Add(-protocol.HeatHotInterval))
	baseline := d.store.PullRequestWatches()[0].LastSuccessAt
	ready.Evidence.Comments = []prreadiness.Comment{
		{ID: "first", Author: "human", Body: "first feedback", CreatedAt: now.Add(time.Second)},
		{ID: "second", Author: "human", Body: "second feedback", CreatedAt: now.Add(time.Second)},
	}
	d.refreshSessionPullRequests(now)
	watch := d.store.PullRequestWatches()[0]
	if watch.LastSuccessAt != baseline || len(watch.FeedbackBaselineIDs) != 0 {
		t.Fatalf("failed enqueue advanced the cursor: %+v", watch)
	}
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 {
		t.Fatalf("first comment was not durably queued: %+v, %v", unread, err)
	}
	if _, err := direct.Exec("DROP TRIGGER reject_second_feedback"); err != nil {
		t.Fatal(err)
	}
	if err := d.store.Close(); err != nil {
		t.Fatal(err)
	}
	d.store, err = store.NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if response := sendPRCommand(t, d, protocol.PullRequestWatchMessage{
		Cmd: protocol.CmdPullRequestWatch, ID: "s1", URL: "https://github.com/victorarias/attn/pull/71", Reviewer: "another-reviewer",
	}); !response.Ok {
		t.Fatalf("change reviewer after interrupted delivery: %+v", response)
	}
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 2 {
		t.Fatalf("retry lost or duplicated feedback: %+v, %v", unread, err)
	}
	if watch := d.store.PullRequestWatches()[0]; len(watch.FeedbackBaselineIDs) != 0 || watch.LastSuccessAt == baseline {
		t.Fatalf("successful retry changed the baseline or did not record success: %+v", watch)
	}
}

func TestPullRequestWatchLifecyclePreservesFeedbackUntilStopped(t *testing.T) {
	url := "https://github.com/victorarias/attn/pull/71"
	for name, command := range map[string]any{
		"unwatch":             protocol.PullRequestUnwatchMessage{Cmd: protocol.CmdPullRequestUnwatch, ID: "s1", URL: url},
		"forget":              protocol.PullRequestForgetMessage{Cmd: protocol.CmdPullRequestForget, ID: "s1", URL: url},
		"interrupted unwatch": protocol.PullRequestUnwatchMessage{Cmd: protocol.CmdPullRequestUnwatch, ID: "s1", URL: url},
		"interrupted forget":  protocol.PullRequestForgetMessage{Cmd: protocol.CmdPullRequestForget, ID: "s1", URL: url},
		"reviewer change":     protocol.PullRequestWatchMessage{Cmd: protocol.CmdPullRequestWatch, ID: "s1", URL: url, Reviewer: "another-reviewer"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newPRDaemonForTest(t, "s1")
			watchPRForRefresh(t, d, "s1", url)
			now := time.Now()
			ready := watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
			serveHost(d, "github.com", &fakePRHost{readiness: ready})
			d.refreshSessionPullRequests(now.Add(-protocol.HeatHotInterval))
			ready.Evidence.Comments = []prreadiness.Comment{{ID: "human", Author: "human", Body: "feedback", CreatedAt: now.Add(time.Second)}}
			d.refreshSessionPullRequests(now)
			if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 2 {
				t.Fatalf("status and feedback were not queued: %+v, %v", unread, err)
			}
			interrupted := strings.HasPrefix(name, "interrupted")
			if interrupted {
				watch := d.store.PullRequestWatches()[0]
				if _, err := d.store.UnwatchPullRequest("s1", watch.PRID); err != nil {
					t.Fatal(err)
				}
				if name == "interrupted forget" {
					if _, err := d.store.ForgetSessionPullRequest("s1", watch.PRID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if response := sendPRCommand(t, d, command); !response.Ok && !interrupted {
				t.Fatalf("command failed: %+v", response)
			}
			wantUnread := 0
			if name == "reviewer change" {
				wantUnread = 1
			}
			if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != wantUnread {
				t.Fatalf("unread feedback after %s = %+v, %v", name, unread, err)
			}
			if name == "reviewer change" {
				d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
				if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "human: feedback") {
					t.Fatalf("reviewer change lost or repeated human feedback: %+v, %v", unread, err)
				}
			}
			if watches := d.store.PullRequestWatches(); name != "reviewer change" && len(watches) != 0 {
				t.Fatalf("stopped watch remains: %+v", watches)
			}
		})
	}
}

func TestPullRequestWatchDoesNotRepeatHumanFeedbackOnNewHead(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	now := time.Now()
	readiness := watchedReadiness("sha-1", prreadiness.ChecksPending, "")
	host := &fakePRHost{readiness: readiness}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(now.Add(-protocol.HeatHotInterval))
	readiness.Evidence.Comments = []prreadiness.Comment{{
		ID: "human-1", Author: "reviewer", Body: "Please check the retry path.", CreatedAt: now.Add(time.Second),
	}}
	d.refreshSessionPullRequests(now)
	if deliveries, _, err := d.store.ReadAgentMailbox("s1", 20, now.Add(2*time.Second)); err != nil || len(deliveries) != 1 {
		t.Fatalf("read first feedback = %+v, %v", deliveries, err)
	}

	host.readiness = watchedReadiness("sha-2", prreadiness.ChecksPending, "")
	host.readiness.Evidence.Comments = readiness.Evidence.Comments
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("old feedback repeated on new head: %+v, %v", unread, err)
	}

	host.readiness.Evidence.Comments = append(host.readiness.Evidence.Comments, prreadiness.Comment{
		ID: "human-2", Author: "reviewer", Body: "The new head needs another guard.", CreatedAt: now.Add(2 * time.Second),
	})
	d.refreshSessionPullRequests(now.Add(2 * protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "another guard") ||
		strings.Contains(unread[0].Item.Prompt, "retry path") {
		t.Fatalf("new feedback delivery = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchIgnoresFetchedObservationAfterUnwatch(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	watches := d.store.PullRequestWatches()
	group := &sessionPullRequestGroup{prID: watches[0].PRID, watches: watches}
	if resp := sendPRCommand(t, d, protocol.PullRequestUnwatchMessage{
		Cmd: protocol.CmdPullRequestUnwatch, ID: "s1", URL: url,
	}); !resp.Ok {
		t.Fatalf("unwatch response = %+v", resp)
	}
	d.processPullRequestWatches(group, watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED"), time.Now())
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("stale fetch recreated notification: %+v, %v", unread, err)
	}
}

func TestPullRequestWatchThumbsUpMustFollowObservedHead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newPRDaemonForTest(t, "s1")
		defer d.store.Close()
		url := "https://github.com/victorarias/attn/pull/71"
		watchPRForRefresh(t, d, "s1", url)
		tickStarted := time.Now().Add(-time.Minute)
		readiness := watchedReadiness("sha-1", prreadiness.ChecksGreen, "")
		readiness.Evidence.Reactions = []prreadiness.Reaction{{
			Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: time.Now().Add(-time.Second),
		}}
		var reactionAt time.Time
		host := &fakePRHost{readiness: readiness, onFetch: func() {
			reactionAt = time.Now()
			time.Sleep(time.Second)
		}}
		serveHost(d, "github.com", host)
		d.refreshSessionPullRequests(tickStarted)
		if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
			t.Fatalf("reaction predating the fetch passed the newly observed head: %+v, %v", unread, err)
		}

		readiness.Evidence.Reactions = append(readiness.Evidence.Reactions, prreadiness.Reaction{
			Author: "chatgpt-codex-connector", Content: "THUMBS_UP", CreatedAt: reactionAt,
		})
		d.refreshSessionPullRequests(tickStarted.Add(protocol.HeatHotInterval))
		if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 ||
			!strings.Contains(unread[0].Item.Prompt, "ready") {
			t.Fatalf("new reaction did not pass observed head: %+v, %v", unread, err)
		}
	})
}

func TestPullRequestWatchArmedWithUnresolvedThreads(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	ready := watchedReadiness("current-head", prreadiness.ChecksGreen, "APPROVED")
	ready.Evidence.Threads = []prreadiness.Thread{
		{ID: "old-codex", Author: "chatgpt-codex-connector", CommitOID: "old-head", Body: "Codex finding"},
		{ID: "old-human", Author: "human-reviewer", CommitOID: "old-head", Body: "Human finding", Location: "watch.go:42"},
	}
	ready.Evidence.Comments = []prreadiness.Comment{{
		ID: "old-human", Author: "human-reviewer", Body: "Human finding", Location: "watch.go:42", CreatedAt: time.Now().Add(time.Hour),
	}}
	serveHost(d, "github.com", &fakePRHost{readiness: ready})
	watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	now := time.Now()
	d.refreshSessionPullRequests(now)
	if status := storedPullRequest(t, d, "s1").ReviewStatus; status != "unresolved_threads" {
		t.Fatalf("review status = %q", status)
	}
	deliveries, _, err := d.store.ReadAgentMailbox("s1", 20, now)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("initial notification = %+v, %v", deliveries, err)
	}
	for _, thread := range ready.Evidence.Threads {
		if !strings.Contains(deliveries[0].Item.Prompt, thread.Body) || !strings.Contains(deliveries[0].Item.Prompt, thread.Location) {
			t.Fatalf("notification omitted %s: %s", thread.ID, deliveries[0].Item.Prompt)
		}
	}
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("unchanged threads repeated: %+v, %v", unread, err)
	}
	ready.Evidence.Threads[0].Resolved = true
	d.refreshSessionPullRequests(now.Add(2 * protocol.HeatHotInterval))
	if status := storedPullRequest(t, d, "s1").ReviewStatus; status != "unresolved_threads" {
		t.Fatalf("remaining human thread did not block: %q", status)
	}
	if _, _, err := d.store.ReadAgentMailbox("s1", 20, now); err != nil {
		t.Fatal(err)
	}
	ready.Evidence.Threads[1].Resolved = true
	d.refreshSessionPullRequests(now.Add(3 * protocol.HeatHotInterval))
	if status := storedPullRequest(t, d, "s1").ReviewStatus; status != prreadiness.ReviewApproved {
		t.Fatalf("resolved threads still block: %q", status)
	}
	deliveries, _, err = d.store.ReadAgentMailbox("s1", 20, now)
	if err != nil || len(deliveries) != 1 || !strings.Contains(deliveries[0].Item.Prompt, "ready") {
		t.Fatalf("ready notification = %+v, %v", deliveries, err)
	}
	d.refreshSessionPullRequests(now.Add(4 * protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("readiness repeated: %+v, %v", unread, err)
	}
}

func TestPullRequestWatchReportsReadyAfterEarlierHumanFeedback(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	now := time.Now()
	pending := watchedReadiness("sha-1", prreadiness.ChecksPending, "")
	host := &fakePRHost{readiness: pending}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(now.Add(-protocol.HeatHotInterval))
	pending.Evidence.Comments = []prreadiness.Comment{{
		ID: "human", Author: "reviewer", Body: "Please check the retry path.", CreatedAt: now.Add(time.Second),
	}}
	d.refreshSessionPullRequests(now)
	if deliveries, _, err := d.store.ReadAgentMailbox("s1", 20, now.Add(time.Second)); err != nil || len(deliveries) != 1 {
		t.Fatalf("read feedback = %+v, %v", deliveries, err)
	}

	host.readiness = watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
	host.readiness.Evidence.Comments = pending.Evidence.Comments
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "ready") {
		t.Fatalf("ready after feedback = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchReportsNewFindingsWhileChecksRemainFailed(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	now := time.Now()
	failed := watchedReadiness("sha-1", prreadiness.ChecksFailed, "COMMENTED")
	failed.Evidence.FailedChecks = []string{"test"}
	host := &fakePRHost{readiness: failed}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(now)
	if deliveries, _, err := d.store.ReadAgentMailbox("s1", 20, now.Add(time.Second)); err != nil || len(deliveries) != 1 {
		t.Fatalf("read check failure = %+v, %v", deliveries, err)
	}

	failed.Evidence.Reviews[0].Findings = []prreadiness.Finding{{ID: "finding", Body: "new review finding", Location: "a.go:7"}}
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "new review finding") {
		t.Fatalf("finding behind failed checks = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchDeliversHumanReviewerCommentsAndInlineFindings(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	if response := sendPRCommand(t, d, protocol.PullRequestWatchMessage{
		Cmd: protocol.CmdPullRequestWatch, ID: "s1", URL: "https://github.com/o/r/pull/71", Reviewer: "human",
	}); !response.Ok {
		t.Fatalf("arm watch: %+v", response)
	}
	now := time.Now()
	ready := watchedReadiness("head", prreadiness.ChecksGreen, "CHANGES_REQUESTED")
	serveHost(d, "github.com", &fakePRHost{readiness: ready})
	d.refreshSessionPullRequests(now.Add(-protocol.HeatHotInterval))
	ready.Evidence.Reviews[0].Author = "human"
	ready.Evidence.Reviews[0].ID = "review-summary"
	ready.Evidence.Reviews[0].Body = "Check the cancellation behavior"
	ready.Evidence.Reviews[0].Findings = []prreadiness.Finding{{ID: "inline", Author: "human", Body: "Fix the guard", Location: "a.go:7"}}
	ready.Evidence.Threads = []prreadiness.Thread{{ID: "inline", Author: "human", Body: "Fix the guard", Location: "a.go:7"}}
	ready.Evidence.Comments = []prreadiness.Comment{
		{ID: "review-summary", Author: "human", Body: "Check the cancellation behavior", CreatedAt: now},
		{ID: "conversation", Author: "human", Body: "Please update the docs", CreatedAt: now},
		{ID: "inline", Author: "human", Body: "Fix the guard", Location: "a.go:7", CreatedAt: now},
	}
	d.refreshSessionPullRequests(now)
	deliveries, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 4 {
		t.Fatalf("feedback and findings = %+v, %v", deliveries, err)
	}
	var prompts string
	for _, delivery := range deliveries {
		prompts += delivery.Item.Prompt
	}
	if strings.Count(prompts, "Please update the docs") != 1 || strings.Count(prompts, "a.go:7: Fix the guard") != 1 || strings.Count(prompts, "Check the cancellation behavior") != 1 {
		t.Fatalf("missing or duplicate reviewer feedback: %s", prompts)
	}
	ready.Evidence.Reviews[0].Findings = nil
	ready.Evidence.Threads[0].Resolved = true
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 4 {
		t.Fatalf("reviewer feedback repeated: %+v, %v", unread, err)
	}
	ready.Evidence.Reviews[0].State = "APPROVED"
	d.refreshSessionPullRequests(now.Add(2 * protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 4 {
		t.Fatalf("approval lost unread feedback: %+v, %v", unread, err)
	}
	prompts = ""
	for _, delivery := range unread {
		prompts += delivery.Item.Prompt
	}
	if strings.Count(prompts, "a.go:7: Fix the guard") != 1 || strings.Count(prompts, "Please update the docs") != 1 || strings.Count(prompts, "Check the cancellation behavior") != 1 {
		t.Fatalf("approval lost or repeated unread feedback: %s", prompts)
	}
	if _, _, err := d.store.ReadAgentMailbox("s1", 20, now); err != nil {
		t.Fatal(err)
	}
	d.refreshSessionPullRequests(now.Add(3 * protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("read feedback reappeared: %+v, %v", unread, err)
	}
	ready.Evidence.Reviews = append(ready.Evidence.Reviews, prreadiness.Review{
		ID: "follow-up", Author: "human", State: "COMMENTED", CommitOID: "head",
		Body: "One more note", SubmittedAt: now.Add(time.Second),
	})
	ready.Evidence.Comments = append(ready.Evidence.Comments, prreadiness.Comment{
		ID: "follow-up", Author: "human", Body: "One more note", CreatedAt: now.Add(time.Second),
	})
	d.refreshSessionPullRequests(now.Add(4 * protocol.HeatHotInterval))
	if status := storedPullRequest(t, d, "s1").ReviewStatus; status != prreadiness.ReviewApproved {
		t.Fatalf("comment-only review erased approval: %q", status)
	}
	unread, err = d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "One more note") {
		t.Fatalf("comment-only review feedback = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchDeduplicatesReadinessAndRetainsDistinctFindings(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	ready := watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
	ready.Evidence.Reviews[0].Findings = []prreadiness.Finding{
		{ID: "f1", Body: "first finding", Location: "a.go:1"},
		{ID: "f2", Body: "second finding", Location: "b.go:2"},
	}
	ready.Evidence.Threads = []prreadiness.Thread{{
		ID: "f1", Author: "chatgpt-codex-connector", Body: "first finding", Location: "a.go:1",
	}}
	host := &fakePRHost{readiness: ready}
	serveHost(d, "github.com", host)
	now := time.Now()
	d.refreshSessionPullRequests(now)
	deliveries, _, err := d.store.ReadAgentMailbox("s1", 20, now.Add(time.Second))
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("read findings = %+v, %v", deliveries, err)
	}
	if prompt := deliveries[0].Item.Prompt; strings.Count(prompt, "first finding") != 1 || !strings.Contains(prompt, "second finding") {
		t.Fatalf("findings prompt = %q", prompt)
	}
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("unchanged findings notified twice: %+v, %v", unread, err)
	}
}

func TestPullRequestWatchExposesPersistentFailureWithoutStorming(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	persistent, err := store.NewWithDB(filepath.Join(t.TempDir(), "outage.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = d.store.Close()
	d.store = persistent
	t.Cleanup(func() { _ = persistent.Close() })
	registerSessionForPRTest(t, d, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	host := &fakePRHost{readyErr: errors.New("review API unavailable")}
	serveHost(d, "github.com", host)
	direct, err := store.OpenDB(d.store.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	if _, err := direct.Exec(`CREATE TRIGGER reject_outage BEFORE INSERT ON agent_mailbox_items
		BEGIN SELECT RAISE(FAIL, 'injected enqueue failure'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < pullRequestWatchFailureThreshold; i++ {
		d.refreshSessionPullRequests(now.Add(time.Duration(i) * protocol.HeatHotInterval))
	}
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("failed insert queued an outage: %+v, %v", unread, err)
	}
	if _, err := direct.Exec("DROP TRIGGER reject_outage"); err != nil {
		t.Fatal(err)
	}
	d.refreshSessionPullRequests(now.Add(time.Duration(pullRequestWatchFailureThreshold) * protocol.HeatHotInterval))
	entry := onlySessionPullRequest(t, d, "s1")
	if protocol.Deref(entry.WatchError) != "review API unavailable" {
		t.Fatalf("watch error = %+v", entry)
	}
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "monitoring unavailable") {
		t.Fatalf("failure notification = %+v, %v", unread, err)
	}
	if _, _, err := d.store.ReadAgentMailbox("s1", 20, now); err != nil {
		t.Fatal(err)
	}
	d.refreshSessionPullRequests(now.Add(time.Duration(pullRequestWatchFailureThreshold+1) * protocol.HeatHotInterval))
	unread, err = d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 0 {
		t.Fatalf("failure stormed: %+v, %v", unread, err)
	}
	host.readyErr = nil
	host.readiness = watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
	d.refreshSessionPullRequests(now.Add(time.Duration(pullRequestWatchFailureThreshold+2) * protocol.HeatHotInterval))
	unread, err = d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "ready") {
		t.Fatalf("recovered readiness did not replace failure: %+v, %v", unread, err)
	}
}

func TestPullRequestWatchUsesGraphQLRateLimitResource(t *testing.T) {
	for _, initiallyLimited := range []bool{true, false} {
		t.Run(fmt.Sprintf("limited-before-fetch=%t", initiallyLimited), func(t *testing.T) {
			d := newPRDaemonForTest(t, "s1")
			watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
			host := &fakePRHost{limited: initiallyLimited, readyErr: github.ErrRateLimited, limitReset: time.Now().Add(time.Hour)}
			serveHost(d, "github.com", host)
			now := time.Now()
			for i := 0; i < pullRequestWatchFailureThreshold; i++ {
				d.refreshSessionPullRequests(now.Add(time.Duration(i) * protocol.HeatHotInterval))
				host.limited = true
			}
			if host.limitedFor != "graphql" {
				t.Fatalf("rate-limit resource = %q, want graphql", host.limitedFor)
			}
			wantReads := 0
			if !initiallyLimited {
				wantReads = 1
			}
			if host.readyReads != wantReads {
				t.Fatalf("queried rate-limited GitHub %d times", host.readyReads)
			}
			if watch := d.store.PullRequestWatches()[0]; !strings.Contains(watch.LastError, "rate limited") {
				t.Fatalf("rate limit not visible: %+v", watch)
			}
			if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 {
				t.Fatalf("rate-limit outage notification = %+v, %v", unread, err)
			}
		})
	}
}

func TestPullRequestWatchRefreshCoalescesSessionSnapshots(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	for _, number := range []int{71, 72} {
		watchPRForRefresh(t, d, "s1", fmt.Sprintf("https://github.com/o/r/pull/%d", number))
	}
	serveHost(d, "github.com", &fakePRHost{readiness: watchedReadiness("head", prreadiness.ChecksPending, "")})
	cap := captureBroadcasts(d)
	if _, err := d.sessionPullRequestRefreshHandler(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if events := sessionUpdates(cap, "s1"); len(events) != 1 {
		t.Fatalf("one refresh produced %d session snapshots", len(events))
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
	if host.limitedFor != "core" {
		t.Errorf("rate-limit resource = %q, want core", host.limitedFor)
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
