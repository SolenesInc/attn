package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMigration100CarriesPreSeverityNotifications(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newStoreAtVersion(dbPath, 167)
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

	if _, err := s.db.Exec(`SELECT severity FROM notifications`); err == nil {
		t.Fatal("the planted schema already has severity; this test would pass without the migration")
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 100`); err != nil {
		t.Fatalf("unrecord migration 100: %v", err)
	}
	if err := migrateDBThrough(s.db, dbPath, 167); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}

	all := legacyNotifications(t, s)
	if len(all) != 2 {
		t.Fatalf("carried %d rows, want 2", len(all))
	}
	unread := 0
	for _, got := range all {
		if got.severity != string(NotificationInfo) {
			t.Fatalf("carried row %q has severity %q, want info", got.title, got.severity)
		}
		if got.body != "body" || got.detail != "detail" || got.sourceID != "t1" {
			t.Fatalf("carried row %q lost fields: %+v", got.title, got)
		}
		if got.readAt == "" {
			unread++
		}
	}
	if unread != 1 {
		t.Fatalf("unread = %d, want 1", unread)
	}

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 100`); err != nil {
		t.Fatalf("unrecord migration 100 again: %v", err)
	}
	if err := migrateDBThrough(s.db, dbPath, 167); err != nil {
		t.Fatalf("re-run migrateDB: %v", err)
	}
	if again := legacyNotifications(t, s); len(again) != 2 {
		t.Fatalf("re-run changed the feed: %v", again)
	}
}

type legacyNotification struct {
	title, severity, body, detail, sourceID, readAt string
}

func legacyNotifications(t *testing.T, s *Store) []legacyNotification {
	t.Helper()
	rows, err := s.db.Query(`SELECT title, severity, body, detail, source_id, read_at FROM notifications ORDER BY created_at DESC, id DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []legacyNotification
	for rows.Next() {
		var n legacyNotification
		if err := rows.Scan(&n.title, &n.severity, &n.body, &n.detail, &n.sourceID, &n.readAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
