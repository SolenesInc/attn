package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/prreadiness"
)

func newSessionPRStore(t *testing.T) *Store {
	t.Helper()
	s, err := newSeededStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func recordPR(t *testing.T, s *Store, sessionID, prID string, number int, at time.Time) bool {
	t.Helper()
	recorded, err := s.RecordSessionPullRequest(SessionPullRequestRecord{
		SessionID:  sessionID,
		PRID:       prID,
		Repository: "github.com/victorarias/attn",
		Number:     number,
		URL:        "https://github.com/victorarias/attn/pull/1",
	}, at)
	if err != nil {
		t.Fatalf("record %s: %v", prID, err)
	}
	return recorded
}

func TestPullRequestWatchReviewerResetIsAtomic(t *testing.T) {
	s := newSessionPRStore(t)
	now := time.Now()
	prID := "github.com:victorarias/attn#71"
	recordPR(t, s, "s1", prID, 71, now)
	if _, err := s.WatchPullRequest("s1", prID, "first-reviewer", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSessionPullRequestReviewStatus("s1", prID, "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_review_reset BEFORE UPDATE OF review_status ON session_pull_requests
		BEGIN SELECT RAISE(FAIL, 'injected verdict reset failure'); END`); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.WatchPullRequest("s1", prID, "second-reviewer", now.Add(time.Second)); err == nil || changed {
		t.Fatalf("failed reset changed the watch: changed=%v err=%v", changed, err)
	}
	if watch, found := s.PullRequestWatch("s1", prID); !found || watch.Reviewer != "first-reviewer" {
		t.Fatalf("failed reset replaced the reviewer: %+v", watch)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_review_reset"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatchPullRequest("s1", prID, "second-reviewer", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if pr, found := s.SessionPullRequestByID(prID); !found || pr.ReviewStatus != "waiting" {
		t.Fatalf("successful reset retained the old verdict: %+v", pr)
	}
	if err := s.UpdateSessionPullRequestReviewStatus("s1", prID, "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_head_reset BEFORE UPDATE OF review_status ON session_pull_requests
		BEGIN SELECT RAISE(FAIL, 'injected head reset failure'); END`); err != nil {
		t.Fatal(err)
	}
	cursor := prreadiness.Cursor{
		Initialized: true, Reviewer: "second-reviewer", HeadSHA: "head",
		SignalBaselineIDs: []string{"reaction"}, SeenCommentIDs: []string{"comment"},
	}
	if err := s.ApplyPullRequestWatchBaseline("s1", prID, "watch", cursor, true); err == nil {
		t.Fatal("failed head reset changed the watch")
	}
	if watch, found := s.PullRequestWatch("s1", prID); !found || watch.Cursor.Initialized {
		t.Fatalf("failed head reset persisted its baseline: %+v", watch)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_head_reset"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPullRequestWatchBaseline("s1", prID, "watch", cursor, true); err != nil {
		t.Fatalf("retry head reset: %v", err)
	}
	if watch, found := s.PullRequestWatch("s1", prID); !found || watch.Cursor.HeadSHA != "head" ||
		strings.Join(watch.Cursor.SignalBaselineIDs, ",") != "reaction" ||
		strings.Join(watch.Cursor.SeenCommentIDs, ",") != "comment" {
		t.Fatalf("successful head reset lost its baseline: %+v", watch)
	}
	if err := s.UpdateSessionPullRequestReviewStatus("s1", prID, "approved"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSessionPullRequestChecked(prID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_unwatch_reset BEFORE UPDATE OF review_status ON session_pull_requests
		WHEN NEW.review_status = '' BEGIN SELECT RAISE(FAIL, 'injected unwatch reset failure'); END`); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.UnwatchPullRequest("s1", prID); err == nil || changed {
		t.Fatalf("failed unwatch changed the watch: changed=%v err=%v", changed, err)
	}
	if _, found := s.PullRequestWatch("s1", prID); !found {
		t.Fatal("failed unwatch deleted the watch")
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_unwatch_reset"); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.UnwatchPullRequest("s1", prID); err != nil || !changed {
		t.Fatalf("retry unwatch: changed=%v err=%v", changed, err)
	}
	if pr, found := s.SessionPullRequestByID(prID); !found || pr.ReviewStatus != "" || pr.StatusCheckedAt != "" {
		t.Fatalf("successful unwatch retained watch-owned state: %+v", pr)
	}
}

func TestSessionPullRequestsComeBackNewestFirst(t *testing.T) {
	s := newSessionPRStore(t)
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	recordPR(t, s, "s1", "github.com:victorarias/attn#1", 1, base)
	recordPR(t, s, "s1", "github.com:victorarias/attn#2", 2, base.Add(time.Minute))
	recordPR(t, s, "s2", "github.com:victorarias/attn#3", 3, base.Add(2*time.Minute))

	got := s.ListSessionPullRequests("s1")
	if len(got) != 2 || got[0].Number != 2 || got[1].Number != 1 {
		t.Fatalf("list = %+v, want #2 then #1", got)
	}

	bySession := s.ListSessionPullRequestsBySession()
	if len(bySession["s1"]) != 2 || bySession["s1"][0].Number != 2 {
		t.Errorf("s1 = %+v, want the same order as the single-session read", bySession["s1"])
	}
	if len(bySession["s2"]) != 1 || bySession["s2"][0].Number != 3 {
		t.Errorf("s2 = %+v, want only its own pull request", bySession["s2"])
	}
}

func TestRecordSessionPullRequestIsIdempotentPerSession(t *testing.T) {
	s := newSessionPRStore(t)
	now := time.Now()

	if !recordPR(t, s, "s1", "github.com:victorarias/attn#1", 1, now) {
		t.Fatal("first record reported nothing new")
	}
	if recordPR(t, s, "s1", "github.com:victorarias/attn#1", 1, now.Add(time.Minute)) {
		t.Fatal("second record of the same pull request reported a new row")
	}
	if !recordPR(t, s, "s2", "github.com:victorarias/attn#1", 1, now) {
		t.Fatal("another session recording the same pull request reported nothing new")
	}

	forgotten, err := s.ForgetSessionPullRequest("s1", "github.com:victorarias/attn#1")
	if err != nil || !forgotten {
		t.Fatalf("forget = %v, %v; want it gone", forgotten, err)
	}
	if got := s.ListSessionPullRequests("s1"); len(got) != 0 {
		t.Errorf("s1 = %+v, want empty", got)
	}
	if got := s.ListSessionPullRequests("s2"); len(got) != 1 {
		t.Errorf("s2 = %+v, want its row untouched", got)
	}
}

func TestPullRequestWatchSurvivesStoreRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.db")
	first, err := NewWithDB(path)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	if changed, err := first.WatchPullRequest("session", "github.com:victorarias/attn#71", "reviewer", time.Now()); err != nil || !changed {
		t.Fatalf("watch = %t, %v", changed, err)
	}
	cursor := prreadiness.Cursor{
		Initialized: true, Reviewer: "reviewer", HeadSHA: "head",
		SignalBaselineIDs: []string{"reaction"}, SeenCommentIDs: []string{"comment"}, LastActionKey: "observation",
	}
	if err := first.ApplyPullRequestWatchBaseline(
		"session", "github.com:victorarias/attn#71", "pull-request-watch:test", cursor, true,
	); err != nil {
		t.Fatalf("begin watch head = %v", err)
	}
	if err := first.RecordPullRequestWatchSuccess(
		"session", "github.com:victorarias/attn#71", cursor, prreadiness.ReviewWaiting, time.Now(),
	); err != nil {
		t.Fatalf("record watch baseline: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second, err := NewWithDB(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer second.Close()
	watches := second.PullRequestWatches()
	if len(watches) != 1 || watches[0].SessionID != "session" || watches[0].Reviewer != "reviewer" ||
		strings.Join(watches[0].Cursor.SignalBaselineIDs, ",") != "reaction" ||
		strings.Join(watches[0].Cursor.SeenCommentIDs, ",") != "comment" {
		t.Fatalf("restarted watches = %+v", watches)
	}
}

func TestOpenSessionPullRequestsReferencedBy(t *testing.T) {
	s := newSessionPRStore(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	stamp := now.Format(time.RFC3339Nano)
	addSession := func(id string, state protocol.SessionState) {
		t.Helper()
		if err := s.AddChecked(&protocol.Session{
			ID: id, Label: id, Directory: t.TempDir(), State: state,
			StateSince: stamp, StateUpdatedAt: stamp, LastSeen: stamp,
		}); err != nil {
			t.Fatalf("add session %s: %v", id, err)
		}
	}
	addSession("active", protocol.SessionStateIdle)
	addSession("active-shared", protocol.SessionStateIdle)
	addSession("closed-armed", protocol.SessionStateIdle)
	addSession("closed-unarmed", protocol.SessionStateIdle)
	addSession("recoverable-armed", protocol.SessionStateRecoverable)
	addSession("recoverable-unarmed", protocol.SessionStateRecoverable)
	if closed, err := s.CloseSession("closed-armed", SessionClose{}, now); err != nil || !closed {
		t.Fatalf("close armed session = %t, %v", closed, err)
	}
	if closed, err := s.CloseSession("closed-unarmed", SessionClose{}, now); err != nil || !closed {
		t.Fatalf("close unarmed session = %t, %v", closed, err)
	}

	recordPR(t, s, "active", "github.com:owner/repo#1", 1, now)
	recordPR(t, s, "active-shared", "github.com:owner/repo#2", 2, now.Add(time.Second))
	recordPR(t, s, "closed-armed", "github.com:owner/repo#2", 2, now.Add(2*time.Second))
	recordPR(t, s, "recoverable-armed", "github.com:owner/repo#3", 3, now.Add(3*time.Second))
	recordPR(t, s, "orphan", "github.com:owner/repo#4", 4, now.Add(4*time.Second))
	recordPR(t, s, "closed-unarmed", "github.com:owner/repo#5", 5, now.Add(5*time.Second))
	recordPR(t, s, "recoverable-unarmed", "github.com:owner/repo#6", 6, now.Add(6*time.Second))
	recordPR(t, s, "orphan", "github.com:owner/repo#7", 7, now.Add(7*time.Second))
	if err := s.UpdateSessionPullRequestStatus("github.com:owner/repo#7", SessionPullRequestStatus{State: "merged"}, now); err != nil {
		t.Fatalf("close referenced PR: %v", err)
	}

	schema := docstore.CollectionSchema{
		Namespace: "test/garden", Collection: "seeds",
		Fields: []docstore.FieldSpec{{Name: "harvest_when_pull_request", Type: docstore.FieldString}},
	}
	if _, err := s.DefineDocumentCollection(schema, now); err != nil {
		t.Fatalf("define seeds: %v", err)
	}
	declared, found, err := s.DocumentCollection(schema.Namespace, schema.Collection)
	if err != nil || !found {
		t.Fatalf("read seeds declaration = found %t, %v", found, err)
	}
	for i, prID := range []string{
		"github.com:owner/repo#2",
		"github.com:owner/repo#3",
		"github.com:owner/repo#4",
		"github.com:owner/repo#7",
	} {
		body := []byte(`{"harvest_when_pull_request":"` + prID + `"}`)
		if _, err := s.PutDocument(*declared, fmt.Sprintf("seed-%d", i), body, now, nil); err != nil {
			t.Fatalf("put seed for %s: %v", prID, err)
		}
	}
	if _, err := s.PutDocument(*declared, "seed-unreadable", []byte("[]"), now, nil); err != nil {
		t.Fatalf("put unrelated malformed seed: %v", err)
	}

	got, err := s.OpenSessionPullRequestsReferencedBy(*declared, "harvest_when_pull_request")
	if err != nil {
		t.Fatalf("refreshable PRs: %v", err)
	}
	want := map[string]bool{
		"active|github.com:owner/repo#1":            true,
		"active-shared|github.com:owner/repo#2":     true,
		"closed-armed|github.com:owner/repo#2":      true,
		"recoverable-armed|github.com:owner/repo#3": true,
		"orphan|github.com:owner/repo#4":            true,
	}
	if len(got) != len(want) {
		t.Fatalf("refreshable PRs = %+v, want %v", got, want)
	}
	for _, rec := range got {
		key := rec.SessionID + "|" + rec.PRID
		if !want[key] {
			t.Errorf("unexpected refreshable PR %s", key)
		}
	}
	if _, err := s.PutDocument(*declared, "seed-0", []byte(`{"harvest_when_pull_request":""}`), now, nil); err != nil {
		t.Fatalf("clear seed condition: %v", err)
	}
	cleared, err := s.OpenSessionPullRequestsReferencedBy(*declared, "harvest_when_pull_request")
	if err != nil {
		t.Fatalf("refreshable PRs after clear: %v", err)
	}
	for _, rec := range cleared {
		if rec.SessionID == "closed-armed" {
			t.Fatalf("cleared condition kept the closed session refreshable: %+v", rec)
		}
	}

	planRows, err := s.db.Query("EXPLAIN QUERY PLAN "+openSessionPullRequestsReferencedByQuery(
		declared.Table, docstore.FieldColumn("harvest_when_pull_request")), protocol.SessionStateRecoverable)
	if err != nil {
		t.Fatalf("explain refreshable PRs: %v", err)
	}
	defer planRows.Close()
	var plan []string
	for planRows.Next() {
		var id, parent, unused int
		var detail string
		if err := planRows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	t.Logf("refreshable PR query plan:\n%s", joined)
	index := fieldIndexName(declared.Table, docstore.FieldColumn("harvest_when_pull_request"))
	if !strings.Contains(joined, "SEARCH "+declared.Table) || !strings.Contains(joined, index) {
		t.Fatalf("seed reference lookup did not use %s:\n%s", index, joined)
	}
}

func TestSessionPullRequestByIDTakesTheFreshestRowAcrossSessions(t *testing.T) {
	s := newSessionPRStore(t)
	base := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	prID := "github.com:victorarias/attn#113"
	recordPR(t, s, "s1", prID, 113, base)
	recordPR(t, s, "s2", prID, 113, base.Add(time.Minute))

	if _, found := s.SessionPullRequestByID("github.com:victorarias/attn#999"); found {
		t.Fatal("a pull request nobody recorded came back found")
	}

	if err := s.UpdateSessionPullRequestStatus(prID, SessionPullRequestStatus{
		Title: "Harvest a seed when its pull request merges", State: "merged",
	}, base.Add(2*time.Minute)); err != nil {
		t.Fatalf("update status: %v", err)
	}
	rec, found := s.SessionPullRequestByID(prID)
	if !found {
		t.Fatal("the recorded pull request came back missing")
	}
	if rec.State != "merged" || rec.Title != "Harvest a seed when its pull request merges" {
		t.Fatalf("row = %+v, want the merged status", rec)
	}

	if _, err := s.ForgetSessionPullRequest("s2", prID); err != nil {
		t.Fatalf("forget: %v", err)
	}
	rec, found = s.SessionPullRequestByID(prID)
	if !found || rec.SessionID != "s1" {
		t.Fatalf("row = %+v found=%v, want s1 still answering", rec, found)
	}
}
