package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestExternalWrapperMigrationPreservesManagedDaysAndProcessReceiptsOnReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v160.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
 INSERT INTO schema_migrations VALUES (160, '2026-10-01');
 CREATE TABLE sessions (id TEXT PRIMARY KEY, launch_intent TEXT NOT NULL DEFAULT '');
 INSERT INTO sessions VALUES ('managed', '{"agent":"claude"}'), ('external', '');`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigration161(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var identity, intent string
	if err := db.QueryRow("SELECT external_process, launch_intent FROM sessions WHERE id='managed'").Scan(&identity, &intent); err != nil || identity != "" || intent != `{"agent":"claude"}` {
		t.Fatalf("managed receipt after upgrade = %q, %q, %v", identity, intent, err)
	}
	const receipt = `{"pid":123,"start_token":"boot:start"}`
	if _, err := db.Exec("UPDATE sessions SET external_process=? WHERE id='external'", receipt); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec("DELETE FROM schema_migrations WHERE version=161"); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigration161(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT external_process FROM sessions WHERE id='external'").Scan(&identity); err != nil || identity != receipt {
		t.Fatalf("external receipt after replay = %q, %v", identity, err)
	}
}
