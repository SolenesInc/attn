package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	var through []migration
	for _, m := range timestamps() {
		if m.version <= version {
			through = append(through, m)
		}
	}
	timestampedMigrations = func() []migration { return through }
	for i, m := range all {
		if m.version > version {
			migrations = all[:i]
			break
		}
	}
	return run()
}

func registerOnlyTestMigrations(t *testing.T) func(migration) {
	original := timestampedMigrations
	t.Cleanup(func() { timestampedMigrations = original })
	var registered []migration
	timestampedMigrations = func() []migration { return registered }
	return func(m migration) { registered = appendMigration(registered, m) }
}

func creatingDBThrough(version int, create func() error) error {
	return withMigrationsThrough(version, func() error {
		if err := upgradeClosestSchemaImage(); err != nil {
			return err
		}
		return create()
	})
}

func upgradeClosestSchemaImage() error {
	version := LatestSchemaVersion()
	if _, built := replayedImages.Load(version); built {
		return nil
	}
	closest := 0
	replayedImages.Range(func(key, _ any) bool {
		if lower := key.(int); lower < version && lower > closest {
			closest = lower
		}
		return true
	})
	if closest == 0 {
		return nil
	}
	build, _ := replayedImages.Load(closest)
	lower, err := build.(func() ([]byte, error))()
	if err != nil {
		return err
	}
	image, err := upgradedSchemaImage(lower)
	if err != nil {
		return fmt.Errorf("upgrading the schema image of %d to %d: %w", closest, version, err)
	}
	replayedImages.Store(version, func() ([]byte, error) { return image, nil })
	return nil
}

func upgradedSchemaImage(lower []byte) ([]byte, error) {
	db, _ := connectSQLite(":memory:")
	defer db.Close()
	if err := withImage(lower, func(fixedSize *sql.DB) error { return backup(fixedSize, db) }); err != nil {
		return nil, err
	}
	if _, err := upgradeSchema(db, ":memory:"); err != nil {
		return nil, err
	}
	return serialize(db)
}

func openDBAtVersion(path string, version int) (db *sql.DB, err error) {
	err = creatingDBThrough(version, func() error {
		var e error
		db, e = OpenDB(path)
		return e
	})
	return
}

func newStoreAtVersion(path string, version int) (s *Store, err error) {
	err = creatingDBThrough(version, func() error {
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
