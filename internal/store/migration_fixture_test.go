package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration fixtures run serially; restore the production migration list before returning.
func withMigrationsThrough(version int, run func() error) error {
	all := migrations
	defer func() { migrations = all }()
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
		s, e = NewWithDB(path)
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
