package daemon

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/github"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
	"github.com/victorarias/attn/internal/store"
)

type fakePRHost struct {
	snapshot   *github.PullRequestSnapshot
	readiness  *prreadiness.Observation
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
}

func (f *fakePRHost) FetchPullRequestReadiness(string, int) (*prreadiness.Observation, error) {
	f.readyReads++
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
	return &github.RateLimitInfo{Resource: resource, ResetAt: f.limitReset}
}

func openSnapshot(title, mergeableState, headSHA string) *github.PullRequestSnapshot {
	return &github.PullRequestSnapshot{
		Number: 71, State: "open", Title: title,
		MergeableState: mergeableState, HeadSHA: headSHA, HeadRef: "pr-status-refresh",
	}
}

func watchedReadiness(head string, checks prreadiness.CheckState, review string) *prreadiness.Observation {
	observation := &prreadiness.Observation{
		Number: 71, State: "open", Title: "Watched pull request", HeadSHA: head, HeadRef: "pr-status-refresh",
		MergeableState: "clean", CheckState: checks,
	}
	if checks != prreadiness.ChecksNone {
		observation.Checks = []prreadiness.Check{{Name: "CI", State: checks}}
	}
	if review != "" {
		observation.Reviews = []prreadiness.Review{{
			ID: "review-" + head, Author: "chatgpt-codex-connector", State: review,
			CommitOID: head, SubmittedAt: time.Now(),
		}}
	}
	return observation
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

func TestSessionPullRequestRefreshTracksUnwatchedStatus(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	recordPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{snapshot: openSnapshot("Fresh", "blocked", "sha-1"), review: "approved"}
	serveHost(d, "github.com", host)
	if fetched, changed := d.refreshSessionPullRequests(time.Now()); fetched != 1 || changed != 1 {
		t.Fatalf("refresh = (%d,%d)", fetched, changed)
	}
	entry := onlySessionPullRequest(t, d, "s1")
	if protocol.Deref(entry.Title) != "Fresh" || string(protocol.Deref(entry.CIStatus)) != "pending" ||
		string(protocol.Deref(entry.ReviewStatus)) != "approved" || host.snapshots != 1 || host.reviews != 1 {
		t.Fatalf("entry = %+v, host = %+v", entry, host)
	}
}

func TestPullRequestWatchSharesFetchAndKeepsReviewerStatusIsolated(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	if resp := sendPRCommand(t, d, protocol.PullRequestWatchMessage{
		Cmd: protocol.CmdPullRequestWatch, ID: "s2", URL: url, Reviewer: "alternate-reviewer",
	}); !resp.Ok {
		t.Fatalf("watch s2 = %+v", resp)
	}
	observation := watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
	observation.Reviews = append(observation.Reviews, prreadiness.Review{
		ID: "alternate", Author: "alternate-reviewer", State: "CHANGES_REQUESTED",
		CommitOID: "sha-1", SubmittedAt: time.Now().Add(time.Second),
	})
	host := &fakePRHost{readiness: observation}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(time.Now())
	if host.readyReads != 1 || storedPullRequest(t, d, "s1").ReviewStatus != "approved" ||
		storedPullRequest(t, d, "s2").ReviewStatus != "changes_requested" {
		t.Fatalf("reads=%d s1=%+v s2=%+v", host.readyReads, storedPullRequest(t, d, "s1"), storedPullRequest(t, d, "s2"))
	}
}

func TestPullRequestWatchBaselinesThenRetriesHumanFeedbackAcrossRestart(t *testing.T) {
	d := newPersistentPRDaemonForTest(t)
	dbPath := d.store.DatabasePath()
	watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	now := time.Now()
	observation := watchedReadiness("sha-1", prreadiness.ChecksPending, "")
	observation.Comments = []prreadiness.Comment{{ID: "existing", Author: "human", Body: "old", CreatedAt: now}}
	host := &fakePRHost{readiness: observation}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(now)
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("first-fetch feedback was delivered: %+v, %v", unread, err)
	}

	direct, err := store.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	if _, err := direct.Exec(`CREATE TRIGGER reject_second_feedback BEFORE INSERT ON agent_mailbox_items
		WHEN NEW.prompt LIKE '%second feedback%' BEGIN SELECT RAISE(FAIL, 'injected enqueue failure'); END`); err != nil {
		t.Fatal(err)
	}
	observation.Comments = append(observation.Comments,
		prreadiness.Comment{ID: "first", Author: "human", Body: "first feedback", CreatedAt: now.Add(time.Second)},
		prreadiness.Comment{ID: "second", Author: "human", Body: "second feedback", CreatedAt: now.Add(time.Second)},
	)
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	watch := d.store.PullRequestWatches()[0]
	if strings.Join(watch.Cursor.SeenCommentIDs, ",") != "existing" {
		t.Fatalf("failed delivery advanced cursor: %+v", watch.Cursor)
	}
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 {
		t.Fatalf("first durable delivery = %+v, %v", unread, err)
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
	d.refreshSessionPullRequests(now.Add(2 * protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 2 || strings.Join(d.store.PullRequestWatches()[0].Cursor.SeenCommentIDs, ",") != "existing,first,second" {
		t.Fatalf("retry = %+v, watch=%+v, err=%v", unread, d.store.PullRequestWatches()[0], err)
	}
}

func TestPullRequestWatchDeliversHumanReviewerVerdictAsDurableFeedback(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	if resp := sendPRCommand(t, d, protocol.PullRequestWatchMessage{
		Cmd: protocol.CmdPullRequestWatch, ID: "s1", URL: url, Reviewer: "human-reviewer",
	}); !resp.Ok {
		t.Fatalf("watch response = %+v", resp)
	}
	now := time.Now()
	observation := watchedReadiness("sha-1", prreadiness.ChecksGreen, "")
	host := &fakePRHost{readiness: observation}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(now)

	observation.Reviews = []prreadiness.Review{{
		ID: "approval", Author: "human-reviewer", State: "APPROVED", CommitOID: "sha-1",
		Body: "Ship it, with this rollout caveat.", SubmittedAt: now.Add(time.Second),
	}}
	observation.Comments = []prreadiness.Comment{{
		ID: "approval", Author: "human-reviewer", Kind: prreadiness.CommentReview, ReviewState: "APPROVED",
		Body: "Ship it, with this rollout caveat.", CreatedAt: now.Add(time.Second),
	}}
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 2 {
		t.Fatalf("deliveries = %+v, %v", unread, err)
	}
	var durable bool
	for _, delivery := range unread {
		if delivery.Item.CoalesceKey == "" && strings.Contains(delivery.Item.Prompt, "rollout caveat") {
			durable = true
		}
	}
	if !durable {
		t.Fatalf("durable formal review feedback missing: %+v", unread)
	}
}

func TestPullRequestWatchRearmRedeliversSameAction(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	first := d.store.PullRequestWatches()[0]
	event := prreadiness.Event{
		ID: "action:unchanged", Kind: prreadiness.EventAction,
		Outcomes: []prreadiness.Outcome{prreadiness.OutcomeChecksFailed}, Details: []string{"CI"},
	}
	if err := d.deliverPullRequestTransition(first, []prreadiness.Event{event}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if read, remaining, err := d.store.ReadAgentMailbox("s1", 1, time.Now()); err != nil || len(read) != 1 || remaining != 0 {
		t.Fatalf("read first action = %+v, %d, %v", read, remaining, err)
	}
	if _, err := d.store.UnwatchPullRequest(first.SessionID, first.PRID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.WatchPullRequest(first.SessionID, first.PRID, first.Reviewer, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second := d.store.PullRequestWatches()[0]
	if err := d.deliverPullRequestTransition(second, []prreadiness.Event{event}, time.Now()); err != nil {
		t.Fatal(err)
	}
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || unread[0].Item.ID == pullRequestWatchEventID(first, event.ID) {
		t.Fatalf("rearmed action = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchRedeliversRecurringAction(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	watch := d.store.PullRequestWatches()[0]
	failed := prreadiness.Observation{
		State: "open", HeadSHA: "sha-1", MergeableState: "clean", CheckState: prreadiness.ChecksFailed,
		Checks: []prreadiness.Check{{Name: "CI", State: prreadiness.ChecksFailed}},
	}
	first := prreadiness.Advance(prreadiness.Cursor{}, failed, watch.Reviewer, prreadiness.StartPolicy{})
	if err := d.deliverPullRequestTransition(watch, first.Events, time.Now()); err != nil {
		t.Fatal(err)
	}
	if read, remaining, err := d.store.ReadAgentMailbox("s1", 1, time.Now()); err != nil || len(read) != 1 || remaining != 0 {
		t.Fatalf("read first failure = %+v, %d, %v", read, remaining, err)
	}
	recovered := failed
	recovered.CheckState = prreadiness.ChecksPending
	recovered.Checks[0].State = prreadiness.ChecksPending
	clear := prreadiness.Advance(first.NextCursor, recovered, watch.Reviewer, prreadiness.StartPolicy{})
	if err := d.deliverPullRequestTransition(watch, clear.Events, time.Now()); err != nil {
		t.Fatal(err)
	}
	recurred := prreadiness.Advance(clear.NextCursor, failed, watch.Reviewer, prreadiness.StartPolicy{})
	if err := d.deliverPullRequestTransition(watch, recurred.Events, time.Now()); err != nil {
		t.Fatal(err)
	}
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || unread[0].Item.ID != pullRequestWatchEventID(watch, recurred.Events[0].ID) {
		t.Fatalf("recurring action = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchRedeliversDirectActionCycle(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	watch := d.store.PullRequestWatches()[0]
	failedA := prreadiness.Observation{
		State: "open", HeadSHA: "sha-1", MergeableState: "clean", CheckState: prreadiness.ChecksFailed,
		Checks: []prreadiness.Check{{Name: "A", State: prreadiness.ChecksFailed}},
	}
	first := prreadiness.Advance(prreadiness.Cursor{}, failedA, watch.Reviewer, prreadiness.StartPolicy{})
	if err := d.deliverPullRequestTransition(watch, first.Events, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.store.ReadAgentMailbox("s1", 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	failedB := failedA
	failedB.Checks = []prreadiness.Check{{Name: "B", State: prreadiness.ChecksFailed}}
	second := prreadiness.Advance(first.NextCursor, failedB, watch.Reviewer, prreadiness.StartPolicy{})
	if err := d.deliverPullRequestTransition(watch, second.Events, time.Now()); err != nil {
		t.Fatal(err)
	}
	third := prreadiness.Advance(second.NextCursor, failedA, watch.Reviewer, prreadiness.StartPolicy{})
	if err := d.deliverPullRequestTransition(watch, third.Events, time.Now()); err != nil {
		t.Fatal(err)
	}
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "\n- A\n") ||
		strings.Contains(unread[0].Item.Prompt, "\n- B\n") {
		t.Fatalf("cycled action = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchBroadcastsClearedUnwatchedVerdict(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	recordPRForRefresh(t, d, "s2", url)
	prID := d.store.PullRequestWatches()[0].PRID
	now := time.Now()
	host := &fakePRHost{readiness: watchedReadiness("sha-1", prreadiness.ChecksPending, "")}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(now)
	if err := d.store.UpdateSessionPullRequestReviewStatus("s2", prID, "approved"); err != nil {
		t.Fatal(err)
	}
	before := len(docFacts(t, d, FactSessionPullRequestChanged))
	if fetched, changed := d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval)); fetched != 1 || changed != 0 {
		t.Fatalf("refresh = (%d,%d)", fetched, changed)
	}
	if got := storedPullRequest(t, d, "s2").ReviewStatus; got != "" {
		t.Fatalf("unwatched review status = %q", got)
	}
	facts := docFacts(t, d, FactSessionPullRequestChanged)[before:]
	seen := map[string]int{}
	for _, fact := range facts {
		seen[fact.Subject]++
	}
	if seen["s1"] != 1 || seen["s2"] != 1 {
		t.Fatalf("refresh facts = %+v", facts)
	}
}

func TestPullRequestWatchClearsStaleActionOnHeadChangeAndDisarmsOnClose(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	watchPRForRefresh(t, d, "s1", url)
	host := &fakePRHost{readiness: watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")}
	serveHost(d, "github.com", host)
	now := time.Now()
	d.refreshSessionPullRequests(now)
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 ||
		!strings.Contains(unread[0].Item.Prompt, "ready") {
		t.Fatalf("ready = %+v, %v", unread, err)
	}
	host.readiness = watchedReadiness("sha-2", prreadiness.ChecksPending, "")
	d.refreshSessionPullRequests(now.Add(protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 0 {
		t.Fatalf("stale action survived: %+v, %v", unread, err)
	}
	host.readiness.State = "closed"
	d.refreshSessionPullRequests(now.Add(2 * protocol.HeatHotInterval))
	if watches := d.store.PullRequestWatches(); len(watches) != 0 {
		t.Fatalf("closed watch remained armed: %+v", watches)
	}
}

func TestPullRequestWatchExposesPersistentFailureAndRecovers(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{readyErr: errors.New("review API unavailable")}
	serveHost(d, "github.com", host)
	now := time.Now()
	for i := 0; i < pullRequestWatchFailureThreshold; i++ {
		d.refreshSessionPullRequests(now.Add(time.Duration(i) * protocol.HeatHotInterval))
	}
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 ||
		!strings.Contains(unread[0].Item.Prompt, "monitoring unavailable") {
		t.Fatalf("outage = %+v, %v", unread, err)
	}
	host.readyErr = nil
	host.readiness = watchedReadiness("sha-1", prreadiness.ChecksGreen, "COMMENTED")
	d.refreshSessionPullRequests(now.Add(time.Duration(pullRequestWatchFailureThreshold) * protocol.HeatHotInterval))
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 ||
		!strings.Contains(unread[0].Item.Prompt, "ready") {
		t.Fatalf("recovery = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchReemitsActionClearedByOutage(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	failed := watchedReadiness("sha-1", prreadiness.ChecksFailed, "")
	host := &fakePRHost{readiness: failed}
	serveHost(d, "github.com", host)
	now := time.Now()
	d.refreshSessionPullRequests(now)
	if unread, err := d.store.UnreadAgentMailboxDeliveries("s1"); err != nil || len(unread) != 1 ||
		!strings.Contains(unread[0].Item.Prompt, "checks failed") {
		t.Fatalf("initial failure = %+v, %v", unread, err)
	}
	host.readyErr = errors.New("review API unavailable")
	for i := 1; i <= pullRequestWatchFailureThreshold; i++ {
		d.refreshSessionPullRequests(now.Add(time.Duration(i) * protocol.HeatHotInterval))
	}
	if watch := d.store.PullRequestWatches()[0]; watch.Cursor.LastActionKey != "" || watch.Cursor.ActionGeneration != 1 {
		t.Fatalf("outage cursor = %+v", watch.Cursor)
	}
	host.readyErr = nil
	d.refreshSessionPullRequests(now.Add(time.Duration(pullRequestWatchFailureThreshold+1) * protocol.HeatHotInterval))
	unread, err := d.store.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(unread) != 1 || !strings.Contains(unread[0].Item.Prompt, "checks failed") {
		t.Fatalf("recovered failure = %+v, %v", unread, err)
	}
}

func TestPullRequestWatchUsesGraphQLRateLimitResource(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	watchPRForRefresh(t, d, "s1", "https://github.com/victorarias/attn/pull/71")
	host := &fakePRHost{limited: true, limitReset: time.Now().Add(time.Hour)}
	serveHost(d, "github.com", host)
	d.refreshSessionPullRequests(time.Now())
	if host.limitedFor != "graphql" || host.readyReads != 0 {
		t.Fatalf("resource=%q reads=%d", host.limitedFor, host.readyReads)
	}
}
