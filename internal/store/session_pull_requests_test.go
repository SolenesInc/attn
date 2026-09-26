package store

import (
	"path/filepath"
	"testing"
	"time"
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
