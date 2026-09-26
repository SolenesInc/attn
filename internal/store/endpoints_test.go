package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestEndpointMigration34BackfillsBlankInstance(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE endpoints (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			ssh_target TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		);
		INSERT INTO endpoints (id, name, ssh_target, enabled, created_at, updated_at)
		VALUES ('endpoint-1', 'gpu', 'user@host', 1, '2026-01-01', '2026-01-01');
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for v := 1; v <= 33; v++ {
		if _, err := db.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`,
			v,
		); err != nil {
			t.Fatalf("seed migrations: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := openSeededDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db2.Close()

	var instance string
	if err := db2.QueryRow(`SELECT instance FROM endpoints WHERE id = 'endpoint-1'`).Scan(&instance); err != nil {
		t.Fatalf("scan instance: %v", err)
	}
	if instance != "" {
		t.Fatalf("legacy endpoint instance = %q, want empty", instance)
	}

	version, err := GetSchemaVersion(db2)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if version < 34 {
		t.Fatalf("schema version = %d, want >=34", version)
	}
}

func TestMigration151CarriesProfileColumnsIntoInstances(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`
		ALTER TABLE endpoints RENAME COLUMN instance TO profile;
		ALTER TABLE instance_roles RENAME TO profile_roles;
		INSERT INTO endpoints (id, name, ssh_target, enabled, profile, created_at, updated_at)
		VALUES ('endpoint-1', 'gpu', 'user@host', 1, 'dev', '2026-01-01', '2026-01-01');
		INSERT INTO profile_roles (role, session_id) VALUES ('chief_of_staff', 'session-a');
		DELETE FROM schema_migrations WHERE version >= 151;
	`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatal(err)
	}
	if got := s.GetEndpoint("endpoint-1"); got == nil || got.Instance != "dev" {
		t.Fatalf("migrated endpoint = %+v, want instance dev", got)
	}
	if got := s.GetInstanceRole("chief_of_staff"); got != "session-a" {
		t.Fatalf("migrated chief of staff = %q, want session-a", got)
	}
}
