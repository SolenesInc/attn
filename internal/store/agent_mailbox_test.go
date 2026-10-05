package store

import (
	"path/filepath"
	"testing"
)

func TestMigration132SeparatesMailboxReceiptsAndPayloads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec(`
		DROP TABLE inbox_items;
		DROP TABLE peer_messages;
		CREATE TABLE agent_messages (
			id TEXT PRIMARY KEY, sender_session_id TEXT NOT NULL,
			target_session_id TEXT NOT NULL, content TEXT NOT NULL,
			created_at TEXT NOT NULL, delivered_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE garden_seed_bells (
			watcher_session_id TEXT NOT NULL, seed_id TEXT NOT NULL,
			event_kind TEXT NOT NULL, message_id TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL, PRIMARY KEY(watcher_session_id, seed_id)
		);
		INSERT INTO agent_messages VALUES
			('peer-queued', 'sender', 'target', 'queued body', '2026-01-01T00:00:00Z', ''),
			('peer-done', 'sender', 'target', 'done body', '2026-01-01T00:00:01Z', '2026-01-01T00:01:00Z'),
			('maintenance', '', 'target', 'sleep now', '2026-01-01T00:00:02Z', ''),
			('seed-queued', '', 'target', 'old prompt', '2026-01-01T00:00:03Z', ''),
			('seed-notified', '', 'target', 'old prompt', '2026-01-01T00:00:04Z', '2026-01-01T00:01:04Z');
		INSERT INTO garden_seed_bells VALUES
			('target', 's-queued', 'note', 'seed-queued', '2026-01-01T00:00:03Z'),
			('target', 's-notified', 'harvested', 'seed-notified', '2026-01-01T00:00:04Z');
		DELETE FROM schema_migrations WHERE version >= 132;
	`); err != nil {
		t.Fatalf("plant pre-132 schema: %v", err)
	}

	migrated, err := OpenDBAtSchemaVersion(dbPath, 132)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}

	var unread int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_mailbox_items
		WHERE recipient_session_id = 'target' AND read_at = ''
	`).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	if unread != 4 {
		t.Fatalf("unread mailbox items = %d, want queued peer, maintenance and two Garden items", unread)
	}
	var peerNotified, peerRead, seedNotified, seedRead, maintenancePrompt string
	if err := s.db.QueryRow(`SELECT notified_at, read_at FROM agent_mailbox_items WHERE id = 'peer-done'`).Scan(&peerNotified, &peerRead); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT notified_at, read_at FROM agent_mailbox_items WHERE id = 'seed-notified'`).Scan(&seedNotified, &seedRead); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT prompt FROM agent_mailbox_items WHERE id = 'maintenance'`).Scan(&maintenancePrompt); err != nil {
		t.Fatal(err)
	}
	if peerNotified == "" || peerRead == "" || seedNotified == "" || seedRead != "" || maintenancePrompt != "sleep now" {
		t.Fatalf("migration receipts peer=%q/%q seed=%q/%q maintenance=%q",
			peerNotified, peerRead, seedNotified, seedRead, maintenancePrompt)
	}
	for _, table := range []string{"agent_messages", "garden_seed_bells"} {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(new(int)); err == nil {
			t.Fatalf("legacy table %s survived migration", table)
		}
	}
}
