package store

import (
	"path/filepath"
	"testing"
)

func TestDelegationPreferencesMigrationUpgradesPreviousSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newSeededStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
		DROP TABLE delegation_preferences;
		ALTER TABLE delegation_operations DROP COLUMN resolved_preferences;
		DELETE FROM schema_migrations WHERE version >= 138;
	`); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := newSeededStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var count int
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'delegation_preferences'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("delegation_preferences table: count=%d err=%v", count, err)
	}
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delegation_operations') WHERE name = 'resolved_preferences'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("resolved_preferences column: count=%d err=%v", count, err)
	}
}
