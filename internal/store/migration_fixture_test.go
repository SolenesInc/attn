package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/profiles"
)

// Migration fixtures run serially; restore the production migration list before returning.
func withMigrationsThrough(version int, run func() error) error {
	all := migrations
	timestamps := timestampedMigrations
	defer func() { migrations = all; timestampedMigrations = timestamps }()
	timestampedMigrations = nil
	for _, m := range timestamps {
		if m.version <= version {
			timestampedMigrations = append(timestampedMigrations, m)
		}
	}
	for i, m := range all {
		if m.version > version {
			migrations = all[:i]
			break
		}
	}
	return run()
}

func openDBAtVersion(path string, version int) (db *sql.DB, err error) {
	err = withMigrationsThrough(version, func() error {
		var e error
		db, e = OpenDB(path)
		return e
	})
	return
}

func newStoreAtVersion(path string, version int) (s *Store, err error) {
	err = withMigrationsThrough(version, func() error {
		var e error
		s, _, e = openLegacySchemaStore(path)
		return e
	})
	return
}

func migrationFixtureStore(t *testing.T, version int) *Store {
	t.Helper()
	s, err := newStoreAtVersion(filepath.Join(t.TempDir(), "migration.db"), version)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func migrateDBThrough(db *sql.DB, path string, version int) error {
	return withMigrationsThrough(version, func() error { return migrateDB(db, path) })
}

func legacySchemaStore(db *sql.DB, writes *tableWrites, path string) (*Store, error) {
	settings, err := readSettings(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	return newDBStoreWithSettings(db, writes, path, true, settings, nil), nil
}

func openLegacySchemaStore(path string) (*Store, SchemaUpgrade, error) {
	db, writes, upgrade, err := openUpgradedDB(path)
	if err != nil {
		return nil, upgrade, err
	}
	s, err := legacySchemaStore(db, writes, path)
	return s, upgrade, err
}

func openCurrentLegacySchemaStore(path string) (*Store, error) {
	db, writes, err := openCurrentDB(path)
	if err != nil {
		return nil, err
	}
	return legacySchemaStore(db, writes, path)
}

func migrationFixtureProfile(t *testing.T, s *Store, id string) profiles.Profile {
	t.Helper()
	var p profiles.Profile
	query := "SELECT id,name,current_desktop_id,last_used_at,revision,deleted_at FROM profiles"
	var args []any
	if id != "" {
		query += " WHERE id=?"
		args = []any{id}
	} else {
		query += " WHERE deleted_at='' ORDER BY created_at,id LIMIT 1"
	}
	if err := s.db.QueryRow(query, args...).Scan(&p.ID, &p.Name, &p.CurrentDesktopID, &p.LastUsedAt, &p.Revision, &p.DeletedAt); err != nil {
		t.Fatal(err)
	}
	return p
}

func migrationFixtureDocument(t *testing.T, s *Store, schema docstore.CollectionSchema, id string, body []byte, at time.Time) {
	t.Helper()
	stamp := at.UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec("INSERT INTO "+schema.Table+"(id,body,rev,created_at,updated_at) VALUES(?,?,1,?,?)", id, string(body), stamp, stamp); err != nil {
		t.Fatal(err)
	}
}
