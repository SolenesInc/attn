package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestTransitionSessionConversationCommitsBindingAndTranscriptScopedState(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "session-1", protocol.SessionStateWorking)
	if _, err := s.CreateTicket(Ticket{
		ID:       "ticket-1",
		Title:    "Continue the work",
		Assignee: "session-1",
	}, "chief", time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	if changed, err := s.TransitionSessionConversation("session-1", "codex-old", "/transcripts/codex-old.jsonl"); err != nil || !changed {
		t.Fatalf("seed old conversation: changed=%v err=%v", changed, err)
	}
	if err := s.SetTicketResumeSessionID("session-1", "codex-old"); err != nil {
		t.Fatalf("SetTicketResumeSessionID: %v", err)
	}
	s.UpdateSessionActivity("session-1", "working in the old conversation", time.Now(), "old-cursor")

	changed, err := s.TransitionSessionConversation("session-1", "codex-new", "/transcripts/codex-new.jsonl")
	if err != nil {
		t.Fatalf("TransitionSessionConversation: %v", err)
	}
	if !changed {
		t.Fatal("transition reported no change")
	}
	if got := s.GetResumeSessionID("session-1"); got != "codex-new" {
		t.Fatalf("session binding = %q, want codex-new", got)
	}
	if got := s.GetSessionTranscriptPath("session-1"); got != "/transcripts/codex-new.jsonl" {
		t.Fatalf("transcript binding = %q, want new exact path", got)
	}
	if got := s.GetTicketResumeSessionID("session-1"); got != "codex-new" {
		t.Fatalf("ticket binding = %q, want codex-new", got)
	}
	if got := s.GetSessionActivity("session-1"); got != (SessionActivity{}) {
		t.Fatalf("activity after transition = %+v, want zero", got)
	}
	if session := s.Get("session-1"); session.Activity != nil || session.ActivityAt != nil {
		t.Fatalf("session still exposes old activity: %+v", session)
	}
}

func TestTransitionSessionConversationIgnoresRepeatedObservation(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "session-1", protocol.SessionStateWorking)
	if changed, err := s.TransitionSessionConversation("session-1", "codex-current", "/transcripts/current.jsonl"); err != nil || !changed {
		t.Fatalf("seed current conversation: changed=%v err=%v", changed, err)
	}
	s.UpdateSessionActivity("session-1", "still current", time.Now(), "current-cursor")

	changed, err := s.TransitionSessionConversation("session-1", "codex-current", "/transcripts/current.jsonl")
	if err != nil {
		t.Fatalf("TransitionSessionConversation: %v", err)
	}
	if changed {
		t.Fatal("repeated observation reported a transition")
	}
	if got := s.GetSessionActivity("session-1"); got.Line != "still current" || got.Cursor != "current-cursor" {
		t.Fatalf("repeated observation disturbed activity: %+v", got)
	}
}

func TestTransitionSessionConversationUpdatesMovedPathWithoutClearingActivity(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "session-1", protocol.SessionStateWorking)
	if changed, err := s.TransitionSessionConversation("session-1", "codex-current", "/old/root.jsonl"); err != nil || !changed {
		t.Fatalf("seed conversation: changed=%v err=%v", changed, err)
	}
	s.UpdateSessionActivity("session-1", "still current", time.Now(), "current-cursor")

	changed, err := s.TransitionSessionConversation("session-1", "codex-current", "/moved/root.jsonl")
	if err != nil || !changed {
		t.Fatalf("move path: changed=%v err=%v", changed, err)
	}
	if got := s.GetSessionConversation("session-1"); got != (SessionConversation{NativeID: "codex-current", TranscriptPath: "/moved/root.jsonl"}) {
		t.Fatalf("binding after move = %+v", got)
	}
	if got := s.GetSessionActivity("session-1"); got.Line != "still current" || got.Cursor != "current-cursor" {
		t.Fatalf("path move disturbed activity: %+v", got)
	}
}

func TestTransitionSessionConversationRejectsPathlessObservation(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "session-1", protocol.SessionStateWorking)
	if changed, err := s.TransitionSessionConversation("session-1", "codex-current", "/transcripts/current.jsonl"); err != nil || !changed {
		t.Fatalf("seed conversation: changed=%v err=%v", changed, err)
	}

	changed, err := s.TransitionSessionConversation("session-1", "ephemeral-root", "")
	if err != nil || changed {
		t.Fatalf("pathless observation: changed=%v err=%v", changed, err)
	}
	if got := s.GetSessionConversation("session-1"); got != (SessionConversation{NativeID: "codex-current", TranscriptPath: "/transcripts/current.jsonl"}) {
		t.Fatalf("pathless observation replaced binding: %+v", got)
	}
}

func TestSetResumeSessionIDClearsPathWhenIdentityChanges(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "session-1", protocol.SessionStateWorking)
	if changed, err := s.TransitionSessionConversation("session-1", "codex-current", "/transcripts/current.jsonl"); err != nil || !changed {
		t.Fatalf("seed conversation: changed=%v err=%v", changed, err)
	}

	s.SetResumeSessionID("session-1", "codex-next")
	if got := s.GetSessionConversation("session-1"); got != (SessionConversation{NativeID: "codex-next"}) {
		t.Fatalf("binding after identity-only update = %+v, want path cleared", got)
	}
}

func TestTransitionSessionResumeIDKeepsTrustedLaunchWithoutAPath(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "session-1", protocol.SessionStateWorking)
	if _, err := s.CreateTicket(Ticket{ID: "ticket-1", Title: "Resume", Assignee: "session-1"}, "chief", time.Now()); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if changed, err := s.TransitionSessionConversation("session-1", "codex-current", "/transcripts/current.jsonl"); err != nil || !changed {
		t.Fatalf("seed conversation: changed=%v err=%v", changed, err)
	}
	s.UpdateSessionActivity("session-1", "old conversation", time.Now(), "old-cursor")

	changed, err := s.TransitionSessionResumeID("session-1", "codex-next")
	if err != nil || !changed {
		t.Fatalf("trusted resume transition: changed=%v err=%v", changed, err)
	}
	if got := s.GetSessionConversation("session-1"); got != (SessionConversation{NativeID: "codex-next"}) {
		t.Fatalf("binding = %+v, want new ID awaiting an exact path", got)
	}
	if got := s.GetSessionActivity("session-1"); got != (SessionActivity{}) {
		t.Fatalf("old activity survived trusted resume transition: %+v", got)
	}
	if got := s.GetTicketResumeSessionID("session-1"); got != "codex-next" {
		t.Fatalf("ticket resume ID = %q, want codex-next", got)
	}
}

func TestTransitionSessionConversationMirrorsBindingAfterSessionClose(t *testing.T) {
	s := newTurnStore(t)
	if _, err := s.CreateTicket(Ticket{
		ID:       "ticket-1",
		Title:    "Resume later",
		Assignee: "closed-session",
	}, "chief", time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	changed, err := s.TransitionSessionConversation("closed-session", "codex-current", "/transcripts/current.jsonl")
	if err != nil {
		t.Fatalf("TransitionSessionConversation: %v", err)
	}
	if changed {
		t.Fatal("ticket-only persistence reported a live-session transition")
	}
	if got := s.GetTicketResumeSessionID("closed-session"); got != "codex-current" {
		t.Fatalf("ticket binding = %q, want codex-current", got)
	}
}
