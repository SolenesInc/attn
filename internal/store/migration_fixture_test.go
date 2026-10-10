package store

import (
	"database/sql"
	"path/filepath"
	"testing"
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
