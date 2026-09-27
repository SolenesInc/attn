package store

import (
	"path/filepath"
	"testing"
)

const testAnnotations = `[{"id":"a1","message_key":"turn-1","start":4,"end":10,"quote":"parser","emoji":"❓","comment":"why this?"}]`

func TestMigration93KeepsDraftsWrittenBeforeTheNoteExisted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pre-93.db")
	db, err := openSeededDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB setup: %v", err)
	}
	if _, err := db.Exec(`
		ALTER TABLE session_annotation_drafts DROP COLUMN note;
		INSERT INTO session_annotation_drafts
			(session_id, annotations_json, generation, tombstone_generation, updated_at)
			VALUES ('session-1', '` + testAnnotations + `', 6, 0, '2026-08-02T12:00:00Z');
		DELETE FROM schema_migrations WHERE version >= 93;
	`); err != nil {
		t.Fatalf("seed a pre-93 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-93 database: %v", err)
	}

	migrated, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer migrated.Close()

	draft, err := migrated.GetSessionAnnotationDraft("session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if draft.Annotations != testAnnotations || draft.Generation != 6 {
		t.Errorf("draft after migration = %+v, want the marks and generation carried", draft)
	}
	if draft.Note != "" {
		t.Errorf("note = %q, want empty for a draft written before it existed", draft.Note)
	}
}
