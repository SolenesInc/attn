package store

import (
	"path/filepath"
	"testing"
)

func TestReverseMachinesShareTheEmptyDestinationWhileSSHDestinationsStayUnique(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"reverse-1", "reverse-2"} {
		if _, err := s.db.Exec(`INSERT INTO machines (id, name, transport, added_at) VALUES (?, ?, 'reverse', '2026-11-27T00:00:00Z')`, id, id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	if _, err := s.AddOutpost("far", "user@far"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddOutpost("far again", "user@far"); err == nil {
		t.Fatal("a second ssh outpost at user@far was added")
	}
}
