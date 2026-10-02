package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration133IndexesUnreadMailboxFIFO(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-133.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`
		CREATE TABLE agent_mailbox_items(id TEXT PRIMARY KEY, recipient_session_id TEXT,notified_at TEXT,read_at TEXT,created_at TEXT);
		CREATE INDEX idx_agent_mailbox_recipient_queued
			ON agent_mailbox_items(recipient_session_id, notified_at, created_at, id);
		DELETE FROM schema_migrations WHERE version >= 133;
	`); err != nil {
		t.Fatalf("rewind migration 133: %v", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var migrationSQL string
	for _, m := range migrations {
		if m.version == 133 {
			migrationSQL = m.sql
		}
	}
	if _, err := tx.Exec(migrationSQL); err != nil {
		t.Fatalf("migration133: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var oldIndexes int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_agent_mailbox_recipient_queued'
	`).Scan(&oldIndexes); err != nil {
		t.Fatal(err)
	}
	if oldIndexes != 0 {
		t.Fatal("queued mailbox index survived migration 133")
	}
	var indexSQL string
	if err := s.db.QueryRow(`
		SELECT sql FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_agent_mailbox_recipient_unread'
	`).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"recipient_session_id, created_at, id", "WHERE read_at = ''"} {
		if !strings.Contains(indexSQL, want) {
			t.Fatalf("unread index %q does not contain %q", indexSQL, want)
		}
	}
}
