package store

import (
	"github.com/victorarias/attn/internal/protocol"
	"path/filepath"
	"testing"
	"time"
)

func TestMigration81BackfillsOpenTurnsFromStateSince(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration-81.db")
	db, err := openDBAtVersion(dbPath, 167)
	if err != nil {
		t.Fatalf("OpenDB setup: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO sessions (id, label, directory, state, state_since, state_updated_at, last_seen) VALUES
			('waiting',  'Waiting',  '/tmp/a', 'waiting_input',    '2026-07-26T10:00:00Z', '2026-07-26T10:00:00Z', '2026-07-26T10:00:00Z'),
			('approval', 'Approval', '/tmp/b', 'pending_approval', '2026-07-26T11:00:00Z', '2026-07-26T11:00:00Z', '2026-07-26T11:00:00Z'),
			('unknown',  'Unknown',  '/tmp/c', 'unknown',          '2026-07-26T12:00:00Z', '2026-07-26T12:00:00Z', '2026-07-26T12:00:00Z'),
			('working',  'Working',  '/tmp/d', 'working',          '2026-07-26T13:00:00Z', '2026-07-26T13:00:00Z', '2026-07-26T13:00:00Z'),
			('idle',     'Idle',     '/tmp/e', 'idle',             '2026-07-26T14:00:00Z', '2026-07-26T14:00:00Z', '2026-07-26T14:00:00Z');
		ALTER TABLE sessions DROP COLUMN turn_opened_at;
		ALTER TABLE sessions DROP COLUMN turn_settled_at;
		DELETE FROM schema_migrations WHERE version >= 81;
	`); err != nil {
		t.Fatalf("seed pre-81 database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-81 database: %v", err)
	}

	migrated, err := newStoreAtVersion(dbPath, 167)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer migrated.Close()

	for id, want := range map[string]string{
		"waiting":  "2026-07-26T10:00:00Z",
		"approval": "2026-07-26T11:00:00Z",
		"unknown":  "2026-07-26T12:00:00Z",
	} {
		got := migrated.TurnStamps(protocol.SessionID(id)).OpenedAt
		if got.IsZero() {
			t.Errorf("%s: no turn opened by the backfill", id)
			continue
		}
		if got.Format(time.RFC3339) != want {
			t.Errorf("%s: opened_at = %s, want %s (the age it has been waiting)", id, got.Format(time.RFC3339), want)
		}
	}
	for _, id := range []string{"working", "idle"} {
		if !migrated.TurnStamps(protocol.SessionID(id)).OpenedAt.IsZero() {
			t.Errorf("%s: backfill opened a turn for a state that does not open one", id)
		}
	}
}
