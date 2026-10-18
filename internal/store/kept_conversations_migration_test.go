package store

import (
	"path/filepath"
	"testing"
)

func TestConversationPinsAndDeletionActorsSurviveMigrationReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v165.db")
	db, err := OpenDBAtSchemaVersion(path, 165)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO kept_conversations
 (resume_id, agent, source_path, bytes, stored_bytes, copied_at, released_at, deleted_at) VALUES
 ('live', 'claude', '/transcript/live', 100, 50, '2026-10-01', '', ''),
 ('deleted', 'claude', '/transcript/deleted', 200, 75, '2026-09-01', '2026-09-02', '2026-09-16');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(db, path); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"live": "", "deleted": "attn"} {
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
	if err := migrateDB(db, path); err != nil {
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
