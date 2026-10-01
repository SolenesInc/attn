package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestConversationPinsAndDeletionActorsSurviveMigrationReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v165.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
 INSERT INTO schema_migrations VALUES (165, '2026-10-01');
 CREATE TABLE kept_conversations (
 resume_id TEXT NOT NULL, agent TEXT NOT NULL, source_path TEXT NOT NULL,
 bytes INTEGER NOT NULL, stored_bytes INTEGER NOT NULL, copied_at TEXT NOT NULL,
 released_at TEXT NOT NULL DEFAULT '', deleted_at TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (agent, resume_id));
 INSERT INTO kept_conversations VALUES
 ('live', 'claude', '/transcript/live', 100, 50, '2026-10-01', '', ''),
 ('deleted', 'claude', '/transcript/deleted', 200, 75, '2026-09-01', '2026-09-02', '2026-09-16');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateConversationPinsFixture(db); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"live": "", "deleted": "sweep"} {
		var actor string
		if err := db.QueryRow("SELECT deleted_by FROM kept_conversations WHERE resume_id=?", id).Scan(&actor); err != nil || actor != want {
			t.Fatalf("upgrade actor %s=%q, %v; want %q", id, actor, err, want)
		}
	}
	_, err = db.Exec(`INSERT INTO kept_conversation_pins VALUES ('claude', 'live', 'session-live', '2026-10-01');
 UPDATE kept_conversations SET deleted_by='user' WHERE resume_id='deleted';
 DELETE FROM schema_migrations WHERE version=166;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateConversationPinsFixture(db); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var actor, pinned, session string
	if err := db.QueryRow("SELECT deleted_by FROM kept_conversations WHERE resume_id='deleted'").Scan(&actor); err != nil || actor != "user" {
		t.Fatalf("replay overwrote actor: %q, %v", actor, err)
	}
	if err := db.QueryRow("SELECT pinned_at, session_id FROM kept_conversation_pins WHERE resume_id='live'").Scan(&pinned, &session); err != nil || pinned != "2026-10-01" || session != "session-live" {
		t.Fatalf("replay changed pin: %q, %q, %v", pinned, session, err)
	}
}

func migrateConversationPinsFixture(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var migrationSQL string
	for _, m := range migrations {
		if m.version == 160 {
			migrationSQL = m.sql
		}
	}
	if err := applyMigration160(tx, migrationSQL); err != nil {
		return err
	}
	return tx.Commit()
}
