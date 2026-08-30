package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newSessionPRStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
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

// The app puts the newest open pull request on the pane header's line, so the
// store's order is the answer to "which one".
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
	// Two sessions can each open the same pull request; the row is per session.
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
