package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/crew"
)

const newSessionAgentStamp = 1795348015053781

func upgradedNewSessionAgent(t *testing.T, before string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, newSessionAgentStamp-1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DefineDocumentCollection(crew.MembersSchema(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if before != "" {
		if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('new_session_agent', ?)`, before); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateDBThrough(s.db, path, newSessionAgentStamp); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'new_session_agent' AND copied_from = ''`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	return after
}

func TestUpgradingNamesTheHarnessNewSessionsRanOn(t *testing.T) {
	if got := upgradedNewSessionAgent(t, ""); got != "claude" {
		t.Fatalf("new_session_agent is %q after the upgrade, want the Claude Code new sessions ran on", got)
	}
}

func TestUpgradingKeepsTheNewSessionHarnessTheUserPicked(t *testing.T) {
	if got := upgradedNewSessionAgent(t, "codex"); got != "codex" {
		t.Fatalf("new_session_agent became %q, want the user's codex", got)
	}
}

func TestANewDatabaseNamesNoNewSessionHarness(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if agent := s.GetSetting("new_session_agent"); agent != "" {
		t.Fatalf("a new database's new_session_agent is %q, want it unset until the user picks a harness", agent)
	}
}
