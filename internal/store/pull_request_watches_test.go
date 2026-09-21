package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/agentmailbox"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/prreadiness"
)

func TestPullRequestWatchReconfigurationPreservesFeedbackAndRearmBaselinesAgain(t *testing.T) {
	s := newSessionPRStore(t)
	base := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	prID := "github.com:victorarias/attn#303"
	recordPR(t, s, "s1", prID, 303, base)
	_, changed, err := s.WatchPullRequest(SessionPullRequestRecord{SessionID: "s1", PRID: prID}, prreadiness.ModeGreen, "", base)
	if err != nil || !changed {
		t.Fatalf("first watch = %t, %v", changed, err)
	}
	watch, _ := s.PullRequestWatch("s1", prID)
	_, _, err = s.ReconcilePullRequestWatch(PullRequestWatchReconcile{
		SessionID: "s1", PRID: prID, CreatedAt: watch.CreatedAt, Mode: watch.Mode,
		Cursor: prreadiness.Cursor{
			Initialized: true, HeadSHA: "head-a", HeadObservedAt: base,
			SeenFeedbackIDs: []string{"comment-1"}, ThreadStates: map[string]bool{"thread-1": true},
		},
		Status:     SessionPullRequestStatus{State: "open", HeadSHA: "head-a"},
		Evaluation: prreadiness.Evaluation{State: prreadiness.StateWaiting, Reason: "review_required"},
		Health:     "current", At: base.Add(time.Second),
		MailboxItems: []agentmailbox.Item{
			testPullRequestMailboxItem("feedback", "s1", prID, base.Add(time.Second)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, changed, err = s.WatchPullRequest(SessionPullRequestRecord{SessionID: "s1", PRID: prID}, prreadiness.ModeFormalReview, "victor", base.Add(2*time.Second))
	if err != nil || !changed {
		t.Fatalf("reconfigure = %t, %v", changed, err)
	}
	reconfigured, _ := s.PullRequestWatch("s1", prID)
	if !reconfigured.Cursor.Initialized || len(reconfigured.Cursor.SeenFeedbackIDs) != 1 || !reconfigured.Cursor.ThreadStates["thread-1"] {
		t.Fatalf("reconfigured cursor = %+v", reconfigured.Cursor)
	}
	if deliveries, err := s.UnreadAgentMailboxDeliveries("s1"); err != nil || len(deliveries) != 1 || deliveries[0].Item.ID != "feedback" {
		t.Fatalf("feedback after reconfiguration = %+v, %v", deliveries, err)
	}
	_, changed, err = s.WatchPullRequest(SessionPullRequestRecord{SessionID: "s1", PRID: prID}, prreadiness.ModeFormalReview, "VICTOR", base.Add(3*time.Second))
	if err != nil || changed {
		t.Fatalf("idempotent watch = %t, %v", changed, err)
	}

	if stopped, err := s.StopPullRequestWatch("s1", prID); err != nil || !stopped {
		t.Fatalf("stop = %t, %v", stopped, err)
	}
	if _, changed, err := s.WatchPullRequest(SessionPullRequestRecord{SessionID: "s1", PRID: prID}, prreadiness.ModeFormalReview, "victor", base.Add(4*time.Second)); err != nil || !changed {
		t.Fatalf("rearm = %t, %v", changed, err)
	}
	rearmed, _ := s.PullRequestWatch("s1", prID)
	if rearmed.Cursor.Initialized || len(rearmed.Cursor.SeenFeedbackIDs) != 0 || len(rearmed.Cursor.ThreadStates) != 0 {
		t.Fatalf("rearmed cursor = %+v", rearmed.Cursor)
	}
}

func TestWatchPullRequestRecordsAndInstallsAtomically(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		t.Run(fmt.Sprintf("preexisting=%t", preexisting), func(t *testing.T) {
			s := newSessionPRStore(t)
			now := time.Date(2026, 9, 21, 8, 30, 0, 0, time.UTC)
			prID := "github.com:victorarias/attn#303"
			rec := SessionPullRequestRecord{
				SessionID: "s1", PRID: prID, Repository: "github.com/victorarias/attn", Number: 303,
				URL: "https://github.com/victorarias/attn/pull/303",
			}
			if preexisting {
				recordPR(t, s, rec.SessionID, rec.PRID, rec.Number, now)
			}
			if _, err := s.db.Exec(`
				CREATE TRIGGER reject_watch_projection BEFORE UPDATE OF readiness_state ON session_pull_requests
				BEGIN SELECT RAISE(ABORT, 'projection unavailable'); END
			`); err != nil {
				t.Fatal(err)
			}

			if _, _, err := s.WatchPullRequest(rec, prreadiness.ModeGreen, "", now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "projection unavailable") {
				t.Fatalf("watch error = %v", err)
			}
			if _, ok := s.PullRequestWatch(rec.SessionID, rec.PRID); ok {
				t.Fatal("failed watch left a watch row")
			}
			records := s.ListSessionPullRequests(rec.SessionID)
			if preexisting && len(records) != 1 {
				t.Fatalf("preexisting record removed: %+v", records)
			}
			if !preexisting && len(records) != 0 {
				t.Fatalf("failed watch left a pull request record: %+v", records)
			}
		})
	}
}

func TestPullRequestWatchReconcileRollsBackProjectionCursorAndMailboxTogether(t *testing.T) {
	s := newSessionPRStore(t)
	now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	prID := "github.com:victorarias/attn#303"
	recordPR(t, s, "s1", prID, 303, now)
	if _, _, err := s.WatchPullRequest(SessionPullRequestRecord{SessionID: "s1", PRID: prID}, prreadiness.ModeGreen, "", now); err != nil {
		t.Fatal(err)
	}
	watch, _ := s.PullRequestWatch("s1", prID)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_pr_mailbox BEFORE INSERT ON agent_mailbox_items BEGIN SELECT RAISE(ABORT, 'mailbox unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	item := testPullRequestMailboxItem("ready", "s1", prID, now)
	_, _, err := s.ReconcilePullRequestWatch(PullRequestWatchReconcile{
		SessionID: "s1", PRID: prID, CreatedAt: watch.CreatedAt, Mode: watch.Mode,
		Cursor:     prreadiness.Cursor{Initialized: true, HeadSHA: "head-a", LastAction: "ready"},
		Status:     SessionPullRequestStatus{State: "open", HeadSHA: "head-a"},
		Evaluation: prreadiness.Evaluation{State: prreadiness.StateReady, Reason: "green"},
		Health:     "current", MailboxItems: []agentmailbox.Item{item}, At: now.Add(time.Second),
	})
	if err == nil || !strings.Contains(err.Error(), "mailbox unavailable") {
		t.Fatalf("reconcile error = %v", err)
	}
	after, _ := s.PullRequestWatch("s1", prID)
	if after.Cursor.Initialized || after.LastSuccessAt != "" {
		t.Fatalf("watch advanced after rollback: %+v", after)
	}
	records := s.ListSessionPullRequests("s1")
	if len(records) != 1 || records[0].ReadinessState != "" || records[0].HeadSHA != "" {
		t.Fatalf("projection advanced after rollback: %+v", records)
	}
	if deliveries, err := s.UnreadAgentMailboxDeliveries("s1"); err != nil || len(deliveries) != 0 {
		t.Fatalf("mailbox = %+v, %v", deliveries, err)
	}
}

func TestPullRequestWatchOutageCoalescesAndSuccessfulReconcileRecoversSilently(t *testing.T) {
	s := newSessionPRStore(t)
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	prID := "github.com:victorarias/attn#303"
	recordPR(t, s, "s1", prID, 303, now)
	if _, _, err := s.WatchPullRequest(SessionPullRequestRecord{SessionID: "s1", PRID: prID}, prreadiness.ModeGreen, "", now); err != nil {
		t.Fatal(err)
	}
	watch, _ := s.PullRequestWatch("s1", prID)
	outage := testPullRequestMailboxItem("outage", "s1", prID, now)
	outage.CoalesceKey = PullRequestWatchOutageCoalesceKey(prID)
	first, err := s.RecordPullRequestWatchFailure("s1", prID, watch.CreatedAt, watch.Mode, watch.Reviewer, "GitHub unavailable", outage, now.Add(time.Second))
	if err != nil || first == nil {
		t.Fatalf("first outage = %+v, %v", first, err)
	}
	second, err := s.RecordPullRequestWatchFailure("s1", prID, watch.CreatedAt, watch.Mode, watch.Reviewer, "still unavailable", outage, now.Add(2*time.Second))
	if err != nil || second != nil {
		t.Fatalf("second outage = %+v, %v", second, err)
	}
	ready := testPullRequestMailboxItem("ready", "s1", prID, now.Add(3*time.Second))
	ready.CoalesceKey = PullRequestWatchCoalesceKey(prID)
	_, changed, err := s.ReconcilePullRequestWatch(PullRequestWatchReconcile{
		SessionID: "s1", PRID: prID, CreatedAt: watch.CreatedAt, Mode: watch.Mode,
		Cursor:     prreadiness.Cursor{Initialized: true, HeadSHA: "head-a", LastAction: "ready"},
		Status:     SessionPullRequestStatus{State: "open", HeadSHA: "head-a"},
		Evaluation: prreadiness.Evaluation{State: prreadiness.StateReady, Reason: "green"},
		Health:     "delayed", HealthError: "feedback: review threads unavailable", FeedbackError: "review threads unavailable",
		MailboxItems: []agentmailbox.Item{ready}, At: now.Add(3 * time.Second),
	})
	if err != nil || !changed {
		t.Fatalf("partial recovery = changed:%t, %v", changed, err)
	}
	afterPartial, _ := s.PullRequestWatch("s1", prID)
	if !afterPartial.OutageActive || afterPartial.LastError != "still unavailable" {
		t.Fatalf("watch after partial recovery = %+v", afterPartial)
	}
	deliveries, err := s.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("mailbox after partial recovery = %+v, %v", deliveries, err)
	}

	_, changed, err = s.ReconcilePullRequestWatch(PullRequestWatchReconcile{
		SessionID: "s1", PRID: prID, CreatedAt: watch.CreatedAt, Mode: watch.Mode,
		Cursor:     prreadiness.Cursor{Initialized: true, HeadSHA: "head-a", LastAction: "ready"},
		Status:     SessionPullRequestStatus{State: "open", HeadSHA: "head-a"},
		Evaluation: prreadiness.Evaluation{State: prreadiness.StateReady, Reason: "green"},
		Health:     "current", MailboxItems: []agentmailbox.Item{ready}, At: now.Add(4 * time.Second),
	})
	if err != nil || !changed {
		t.Fatalf("full recovery = changed:%t, %v", changed, err)
	}
	deliveries, err = s.UnreadAgentMailboxDeliveries("s1")
	if err != nil || len(deliveries) != 1 || deliveries[0].Item.ID != "ready" {
		t.Fatalf("mailbox after recovery = %+v, %v", deliveries, err)
	}
	records := s.ListSessionPullRequests("s1")
	if records[0].WatchHealth != "current" || records[0].WatchError != "" {
		t.Fatalf("projection after recovery = %+v", records[0])
	}
	afterRecovery, _ := s.PullRequestWatch("s1", prID)
	if afterRecovery.OutageActive || afterRecovery.LastError != "" {
		t.Fatalf("watch after recovery = %+v", afterRecovery)
	}

	_, changed, err = s.ReconcilePullRequestWatch(PullRequestWatchReconcile{
		SessionID: "s1", PRID: prID, CreatedAt: watch.CreatedAt, Mode: watch.Mode,
		Cursor:     prreadiness.Cursor{Initialized: true, HeadSHA: "head-a", LastAction: "ready"},
		Status:     SessionPullRequestStatus{State: "open", HeadSHA: "head-a"},
		Evaluation: prreadiness.Evaluation{State: prreadiness.StateReady, Reason: "green"},
		Health:     "current", MailboxItems: []agentmailbox.Item{ready}, At: now.Add(5 * time.Second),
	})
	if err != nil || changed {
		t.Fatalf("unchanged poll = changed:%t, %v", changed, err)
	}
}

func testPullRequestMailboxItem(id, sessionID, prID string, at time.Time) agentmailbox.Item {
	return agentmailbox.Item{
		ID: id, RecipientSessionID: sessionID, Kind: agentmailbox.KindMaintenancePrompt,
		SourceID: prID, Prompt: id, CreatedAt: at.UTC().Format(docstore.TimeFormat),
	}
}
