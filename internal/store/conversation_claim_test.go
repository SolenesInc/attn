package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAReportedConversationIsExclusiveInItsProfile(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id, profile := range map[protocol.SessionID]string{"holder": "work", "here": "home", "neighbour": "home"} {
		s.Add(&protocol.Session{ID: id, Label: string(id)})
		if _, err := s.db.Exec(`UPDATE sessions SET profile_id = ? WHERE id = ?`, profile, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.TransitionSessionConversation("holder", "held-in-work", "/t/held-in-work.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionSessionConversation("neighbour", "held-at-home", "/t/held-at-home.jsonl"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		native     string
		wantRefuse bool
	}{
		{"a reported conversation open in its own profile is refused", "held-at-home", true},
		{"a reported conversation open only in another profile binds", "held-in-work", false},
	} {
		_, err := s.ClaimSessionConversation("here", tc.native, "/t/"+tc.native+".jsonl")
		if refused := errors.Is(err, ErrConversationClaimed); refused != tc.wantRefuse || (err != nil && !refused) {
			t.Errorf("%s: err=%v, want refused=%v", tc.name, err, tc.wantRefuse)
		}
	}
	if got := s.GetSessionConversation("here").NativeID; got != "held-in-work" {
		t.Fatalf("session here follows %q, want the conversation its own process reported", got)
	}
}
