package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateDB_PreMigrationBackup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	latest := latestSchemaVersion()
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, latest); err != nil {
		t.Fatalf("unrecord latest migration: %v", err)
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB error: %v", err)
	}

	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "attn-premigration-") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no pre-migration backup found in %s, entries: %v", backupDir, entries)
	}
}
