package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFreshDatabasesMatchTheMigrationChain(t *testing.T) {
	chain, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Close()
	chain.SetMaxOpenConns(1)
	if err := migrateSchema(chain, ":memory:"); err != nil {
		t.Fatal(err)
	}
	want := schemaFingerprint(t, chain)

	for _, path := range []string{":memory:", filepath.Join(t.TempDir(), "attn.db")} {
		db, err := OpenDB(path)
		if err != nil {
			t.Fatalf("OpenDB(%s): %v", path, err)
		}
		if got := schemaFingerprint(t, db); !reflect.DeepEqual(got, want) {
			t.Errorf("OpenDB(%s) schema differs from the migration chain:\n got %v\nwant %v", path, got, want)
		}
		if _, err := db.Exec(`CREATE TABLE growth (payload BLOB)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2000)
			INSERT INTO growth SELECT randomblob(4096) FROM n`); err != nil {
			t.Fatalf("OpenDB(%s) cannot grow past the copied schema: %v", path, err)
		}
		db.Close()
	}
}

func TestAFreshFileDatabaseKeepsItsSchemaAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	want := schemaFingerprint(t, db)
	db.Close()

	reopened, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := schemaFingerprint(t, reopened); !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened schema differs:\n got %v\nwant %v", got, want)
	}
}

func schemaFingerprint(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	rows, err := db.Query(`SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind, name, table, ddl string
		if err := rows.Scan(&kind, &name, &table, &ddl); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s %s %s", kind, name, table, ddl))
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	versions, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer versions.Close()
	for versions.Next() {
		var v int
		if err := versions.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("migration %d", v))
	}
	return out
}
