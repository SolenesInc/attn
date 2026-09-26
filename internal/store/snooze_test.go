package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSnoozedSessionsListsLiveDeadlines(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "deferred", protocol.SessionStateIdle)
	addTurnSession(t, s, "lapsed", protocol.SessionStateIdle)
	addTurnSession(t, s, "awake", protocol.SessionStateIdle)

	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	s.SnoozeTurn("deferred", future, now)
	s.SnoozeTurn("lapsed", past, now)

	snoozed := s.SnoozedSessions()
	if len(snoozed) != 2 {
		t.Fatalf("SnoozedSessions returned %d entries, want 2: %+v", len(snoozed), snoozed)
	}
	if got := snoozed["deferred"].UTC(); !got.Equal(future.UTC()) {
		t.Errorf("deferred deadline = %s, want %s", got, future.UTC())
	}
	if got := snoozed["lapsed"].UTC(); !got.Equal(past.UTC()) {
		t.Errorf("lapsed deadline = %s, want %s", got, past.UTC())
	}
	if _, ok := snoozed["awake"]; ok {
		t.Error("a session that was never snoozed is listed")
	}
}
