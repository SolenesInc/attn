package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

var sessionOwnedTableSeeds = map[string]func(*testing.T, *Store, string){
	"session_annotation_drafts": func(t *testing.T, s *Store, sessionID string) {
		t.Helper()
		if err := s.SaveSessionAnnotationDraft(sessionID, testAnnotations, "", 1, time.Now()); err != nil {
			t.Fatalf("save annotation draft for %s: %v", sessionID, err)
		}
	},
	"session_pull_requests": func(t *testing.T, s *Store, sessionID string) {
		t.Helper()
		_, err := s.RecordSessionPullRequest(SessionPullRequestRecord{
			SessionID:  sessionID,
			PRID:       "github.com:victorarias/attn#" + sessionID,
			Repository: "github.com/victorarias/attn",
			Number:     1,
			URL:        "https://github.com/victorarias/attn/pull/1",
		}, time.Now())
		if err != nil {
			t.Fatalf("record pull request for %s: %v", sessionID, err)
		}
	},
	"session_exit_screens": func(t *testing.T, s *Store, sessionID string) {
		t.Helper()
		if err := s.SaveSessionExitScreen(SessionExitScreen{SessionID: sessionID, Text: "Error: boom", ExitCode: 1}, time.Now()); err != nil {
			t.Fatalf("save exit screen for %s: %v", sessionID, err)
		}
	},
}

func TestEverySessionRemovalPathDropsEverySessionOwnedTable(t *testing.T) {
	tests := []struct {
		name    string
		remove  func(*Store)
		survive string
	}{
		{name: "Remove", remove: func(s *Store) { s.Remove("s1") }, survive: "s2"},
		{name: "ClearSessions", remove: func(s *Store) { s.ClearSessions() }},
		{name: "RemoveSessionsInDirectory", remove: func(s *Store) { s.RemoveSessionsInDirectory("/tmp/one") }},
	}

	for _, table := range sessionOwnedTables {
		seed := sessionOwnedTableSeeds[table]
		if seed == nil {
			t.Fatalf("%s is session-owned but this test cannot seed it; add a seeder so every "+
				"removal path stays covered", table)
		}
		for _, tc := range tests {
			t.Run(table+"/"+tc.name, func(t *testing.T) {
				s := newSessionOwnedTableStore(t)
				addSessionInDirectory(t, s, "s1", "/tmp/one")
				addSessionInDirectory(t, s, "s2", "/tmp/two")
				seed(t, s, "s1")
				seed(t, s, "s2")

				tc.remove(s)

				if rows := countRowsFor(t, s, table, "s1"); rows != 0 {
					t.Errorf("%s rows for s1 after %s = %d, want nothing left behind", table, tc.name, rows)
				}
				if tc.survive != "" {
					if rows := countRowsFor(t, s, table, tc.survive); rows != 1 {
						t.Errorf("%s rows for %s after %s = %d, want its own untouched",
							table, tc.survive, tc.name, rows)
					}
				}
			})
		}
	}
}

func newSessionOwnedTableStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewWithDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func addSessionInDirectory(t *testing.T, s *Store, id, directory string) {
	t.Helper()
	s.Add(&protocol.Session{
		ID:         id,
		Label:      id,
		Directory:  directory,
		State:      protocol.SessionStateIdle,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	})
}

func countRowsFor(t *testing.T, s *Store, table, sessionID string) int {
	t.Helper()
	var rows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE session_id = ?", sessionID).Scan(&rows); err != nil {
		t.Fatalf("count %s for %s: %v", table, sessionID, err)
	}
	return rows
}
