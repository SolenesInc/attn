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

// Seeding a cursor before any line exists has its own writer: routing it through
// UpdateSessionActivity would hit the clear rule and wipe the seed.
func TestSettingTheCursorAloneSurvivesAnEmptyLine(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)

	if !s.SetSessionActivityCursor("s1", "v1:abc:512:0") {
		t.Fatal("seeding a cursor reported no change")
	}
	got := s.GetSessionActivity("s1")
	if got.Cursor != "v1:abc:512:0" {
		t.Errorf("cursor = %q, want the seed to survive", got.Cursor)
	}
	if got.Line != "" || !got.At.IsZero() {
		t.Errorf("seeding invented a line: %+v", got)
	}
	if session := s.Get("s1"); session.Activity != nil {
		t.Errorf("activity = %v on the wire after a seed, want absent", session.Activity)
	}
}

func TestSettingTheCursorKeepsAnExistingLine(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "s1", protocol.SessionStateWorking)
	at := time.Now()
	s.UpdateSessionActivity("s1", "running the frontend test suite", at, "v1:abc:512:0")

	if !s.SetSessionActivityCursor("s1", "v1:abc:2048:0") {
		t.Fatal("advancing the cursor reported no change")
	}
	got := s.GetSessionActivity("s1")
	if got.Line != "running the frontend test suite" {
		t.Errorf("line = %q, want it kept across a cursor advance", got.Line)
	}
	if got.Cursor != "v1:abc:2048:0" {
		t.Errorf("cursor = %q, want the advance", got.Cursor)
	}
	if got.At.IsZero() {
		t.Error("the stamp was dropped, so the line would age as if never generated")
	}
}

func TestSetSessionActivityCursorReportsAMissingSession(t *testing.T) {
	s := newTurnStore(t)
	if s.SetSessionActivityCursor("nobody", "v1:abc:1:0") {
		t.Error("seeding a cursor for a session that does not exist reported a change")
	}
}

func TestUpdateSessionActivityReportsAMissingSession(t *testing.T) {
	s := newTurnStore(t)
	if s.UpdateSessionActivity("nobody", "doing something", time.Now(), "1") {
		t.Error("writing activity for a session that does not exist reported a change")
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
