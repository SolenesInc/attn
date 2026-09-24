package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParentSessionIDRoundTrips(t *testing.T) {
	s := newTurnStore(t)
	addTurnSession(t, s, "agent", protocol.SessionStateIdle)

	now := time.Now().Format(time.RFC3339Nano)
	if err := s.AddChecked(&protocol.Session{
		ID:              "shell",
		Label:           "shell",
		Agent:           protocol.SessionAgentShell,
		Directory:       "/tmp/shell",
		WorkspaceID:     "ws-1",
		State:           protocol.SessionStateIdle,
		StateSince:      now,
		StateUpdatedAt:  now,
		LastSeen:        now,
		ParentSessionID: protocol.Ptr("agent"),
	}); err != nil {
		t.Fatalf("add satellite: %v", err)
	}

	if got := protocol.Deref(s.Get("shell").ParentSessionID); got != "agent" {
		t.Fatalf("parent_session_id = %q, want %q", got, "agent")
	}
	for _, session := range s.List("") {
		if session.ID == "shell" && protocol.Deref(session.ParentSessionID) != "agent" {
			t.Fatalf("parent_session_id = %q from List, want %q", protocol.Deref(session.ParentSessionID), "agent")
		}
	}
}
