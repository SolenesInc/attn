package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMigration100CarriesPreSeverityNotifications(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(`ALTER TABLE notifications DROP COLUMN severity`); err != nil {
		t.Fatalf("drop severity column: %v", err)
	}
	base := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	for i, row := range []struct{ id, title, readAt string }{
		{"n1", "Older failure", base.Format(sortableTimeFormat)},
		{"n2", "Newer failure", ""},
	} {
		if _, err := s.db.Exec(
			`INSERT INTO notifications (id, kind, title, body, detail, source_kind, source_id, created_at, read_at)
			 VALUES (?, 'task_failed', ?, 'body', 'detail', 'task', 't1', ?, ?)`,
			row.id, row.title, base.Add(time.Duration(i)*time.Second).Format(sortableTimeFormat), row.readAt,
		); err != nil {
			t.Fatalf("plant pre-severity row %s: %v", row.id, err)
		}
	}

	if _, err := s.ListNotifications(); err == nil {
		t.Fatal("the planted schema already has severity; this test would pass without the migration")
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 100`); err != nil {
		t.Fatalf("unrecord migration 100: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	all, err := s.ListNotifications()
	if err != nil {
		t.Fatalf("list after migration: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("carried %d rows, want 2", len(all))
	}
	for _, got := range all {
		if got.Severity != NotificationInfo {
			t.Fatalf("carried row %q has severity %q, want info", got.Title, got.Severity)
		}
		if got.Body != "body" || got.Detail != "detail" || got.SourceID != "t1" {
			t.Fatalf("carried row %q lost fields: %+v", got.Title, got)
		}
	}
	unread, err := s.UnreadNotificationCount()
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread = %d, want 1", unread)
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 100`); err != nil {
		t.Fatalf("unrecord migration 100 again: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("re-run migrateDB: %v", err)
	}
	if again, err := s.ListNotifications(); err != nil || len(again) != 2 {
		t.Fatalf("re-run changed the feed: %v (err %v)", again, err)
	}
}
