package store

import (
	"database/sql"
	"encoding/json"
	"github.com/victorarias/attn/internal/docstore"
	"path/filepath"
	"testing"
	"time"
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

func defineLegacyDocumentCollection(s *Store, schema docstore.CollectionSchema, now time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	fields, err := json.Marshal(schema.Fields)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(`INSERT INTO document_collections(namespace,collection,fields_json,updated_at) VALUES(?,?,?,?)`, schema.Namespace, schema.Collection, string(fields), now.UTC().Format(docstore.TimeFormat))
	if err != nil {
		return false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return false, err
	}
	if err := createCollectionTable(tx, docstore.TableName(id), schema.Fields); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func legacyDocumentCollection(s *Store, namespace, collection string) (*docstore.CollectionSchema, bool, error) {
	schema, _, found, err := readCollection(s.db, namespace, collection)
	return &schema, found, err
}
