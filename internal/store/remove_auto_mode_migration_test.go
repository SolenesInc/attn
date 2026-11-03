package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOutpostDatabaseUpgradesThroughMigrationsThatRemoveCopiedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outpost.db")
	db, err := OpenDBAtSchemaVersion(path, 1793552708430693)
	if err != nil {
		t.Fatal(err)
	}
	removed := []string{"installed_bundled_plugins", "automode_enabled_default", "automode_denial_ledger_cursor"}
	err = transactWritingCopies(context.Background(), db, func(tx *sql.Tx) error {
		for _, key := range append(removed, "theme") {
			if _, err := tx.Exec(`INSERT INTO settings(key,value,copied_from) VALUES(?,'1','d-home')`, key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, key := range removed {
		var left int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = ?`, key).Scan(&left); err != nil || left != 0 {
			t.Fatalf("copied setting %s left %d rows: %v", key, left, err)
		}
	}
	if s.CopiedSetting("theme", "d-home") != "1" {
		t.Fatal("the upgrade dropped a copied setting no migration removes")
	}
	if _, err := s.db.Exec(`DELETE FROM settings WHERE key = 'theme'`); err == nil {
		t.Fatal("after the upgrade, copied settings must refuse writes outside the copy applier")
	}
}
