package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

func TestUpdateSessionActivityRoundTripsTheLineAndItsCursor(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	at := time.Date(2026, 8, 9, 14, 30, 0, 0, time.UTC)
	if !s.UpdateSessionActivity("s1", "running the frontend test suite", at, "1024") {
		t.Fatal("update reported no change")
	}

	record := s.GetSessionActivity("s1")
	if record.Line != "running the frontend test suite" {
		t.Errorf("line = %q", record.Line)
	}
	if !record.At.Equal(at) {
		t.Errorf("at = %v, want %v", record.At, at)
	}
	if record.Cursor != "1024" {
		t.Errorf("cursor = %q, want the cursor it was generated through", record.Cursor)
	}
}

func TestGetAndListCarryTheActivityPair(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	at := time.Date(2026, 8, 9, 14, 30, 0, 0, time.UTC)
	s.UpdateSessionActivity("s1", "fixing a failing migration", at, "512")

	session := s.Get("s1")
	if protocol.Deref(session.Activity) != "fixing a failing migration" {
		t.Errorf("Get activity = %q", protocol.Deref(session.Activity))
	}
	if protocol.Deref(session.ActivityAt) != at.Format(docstore.TimeFormat) {
		t.Errorf("Get activity_at = %q", protocol.Deref(session.ActivityAt))
	}

	listed := s.List("")
	if len(listed) != 1 {
		t.Fatalf("List returned %d sessions", len(listed))
	}
	if protocol.Deref(listed[0].Activity) != "fixing a failing migration" {
		t.Errorf("List activity = %q", protocol.Deref(listed[0].Activity))
	}
	if protocol.Deref(listed[0].ActivityAt) != at.Format(docstore.TimeFormat) {
		t.Errorf("List activity_at = %q", protocol.Deref(listed[0].ActivityAt))
	}
}

func TestSessionWithoutAnActivityLineCarriesNeitherField(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	session := s.Get("s1")
	if session.Activity != nil || session.ActivityAt != nil {
		t.Errorf("a session that never generated a line carries activity=%v at=%v", session.Activity, session.ActivityAt)
	}
	if got := s.GetSessionActivity("s1"); got != (SessionActivity{}) {
		t.Errorf("GetSessionActivity = %+v, want zero", got)
	}
}

func TestReAddingASessionKeepsItsActivity(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	at := time.Date(2026, 8, 9, 14, 30, 0, 0, time.UTC)
	s.UpdateSessionActivity("s1", "running the frontend test suite", at, "1024")

	addTurnSession(t, s, "s1", protocol.SessionStateIdle)

	if got := protocol.Deref(s.Get("s1").Activity); got != "running the frontend test suite" {
		t.Errorf("activity = %q after a re-add, want it kept", got)
	}
	if got := s.GetSessionActivity("s1").Cursor; got != "1024" {
		t.Errorf("cursor = %q after a re-add, want it kept", got)
	}
}

func TestClearingActivityAlsoDropsTheCursor(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)
	s.UpdateSessionActivity("s1", "running the frontend test suite", time.Now(), "1024")

	if !s.UpdateSessionActivity("s1", "", time.Time{}, "") {
		t.Fatal("clear reported no change")
	}
	if got := s.GetSessionActivity("s1"); got != (SessionActivity{}) {
		t.Errorf("GetSessionActivity = %+v after a clear, want zero", got)
	}
	session := s.Get("s1")
	if session.Activity != nil || session.ActivityAt != nil {
		t.Errorf("activity=%v at=%v after a clear, want both absent", session.Activity, session.ActivityAt)
	}
}

func TestConversationGuardedActivityWritesRejectAStaleBinding(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)
	s.SetResumeSessionID("s1", "conversation-old")
	s.UpdateSessionActivity("s1", "old work", time.Now(), "old-cursor")

	changed, err := s.TransitionSessionConversation("s1", "conversation-new", "/transcripts/conversation-new.jsonl")
	if err != nil || !changed {
		t.Fatalf("transition: changed=%v err=%v", changed, err)
	}
	if s.UpdateSessionActivityForConversation("s1", "conversation-old", "stale work", time.Now(), "stale-cursor") {
		t.Error("stale conversation restored an activity line")
	}
	if s.SetSessionActivityCursorForConversation("s1", "conversation-old", "stale-cursor") {
		t.Error("stale conversation restored an activity cursor")
	}
	if got := s.GetSessionActivity("s1"); got != (SessionActivity{}) {
		t.Errorf("activity = %+v after stale writes, want the transition clear preserved", got)
	}

	if !s.UpdateSessionActivityForConversation("s1", "conversation-new", "new work", time.Now(), "new-cursor") {
		t.Fatal("current conversation could not write activity")
	}
	if got := s.GetSessionActivity("s1"); got.Line != "new work" || got.Cursor != "new-cursor" {
		t.Errorf("activity = %+v after current write", got)
	}
}
