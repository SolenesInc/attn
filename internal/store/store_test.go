package store

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestMigration130CarriesLegacyIntentionalCloseMark(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-close.db")
	s, err := newStoreAtVersion(dbPath, 167)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Add(&protocol.Session{ID: "legacy-close", Label: "legacy-close"})

	if _, err := s.db.Exec(`UPDATE sessions SET closed_intentionally_at = '2026-09-01T12:00:00Z' WHERE id = 'legacy-close';
		DROP TABLE session_teardown_tombstones;
		DELETE FROM schema_migrations WHERE version = 130`); err != nil {
		t.Fatalf("restore pre-130 schema: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close pre-130 store: %v", err)
	}

	reopened, err := newStoreAtVersion(dbPath, 167)
	if err != nil {
		t.Fatalf("reopen with migration 130: %v", err)
	}
	defer reopened.Close()
	if !reopened.SessionCloseIntentional("legacy-close") {
		t.Fatal("migration 130 did not carry the legacy close mark into the tombstone")
	}
}
