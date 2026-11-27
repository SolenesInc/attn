package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAnUpgradedDatabaseDropsTheHomesCopyOfTheEditorAndCaptureShortcut(t *testing.T) {
	const version = 1795763784029208
	path := filepath.Join(t.TempDir(), "outpost.db")
	db, err := OpenDBAtSchemaVersion(path, version-1)
	if err != nil {
		t.Fatal(err)
	}
	moved := []string{"editor_executable", "capture.shortcut"}
	err = transactWritingCopies(context.Background(), db, func(tx *sql.Tx) error {
		for _, key := range append(moved, "theme") {
			if _, err := tx.Exec(`INSERT INTO settings(key,value,copied_from) VALUES(?,'the home''s','d-home')`, key); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO outpost_outbox(host,tbl,key) VALUES('laptop','settings',?)`, key); err != nil {
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
	for _, key := range moved {
		var copied, queued int
		if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM settings WHERE key = ?), (SELECT COUNT(*) FROM outpost_outbox WHERE tbl = 'settings' AND key = ?)`, key, key).Scan(&copied, &queued); err != nil || copied != 0 || queued != 0 {
			t.Fatalf("%s after the upgrade: %d copied rows and %d queued copies, %v; want neither", key, copied, queued, err)
		}
		if err := s.SetSettingChecked(key, "this machine's"); err != nil {
			t.Fatalf("setting this machine's own %s after the upgrade: %v", key, err)
		}
	}
	if s.CopiedSetting("theme", "d-home") != "the home's" {
		t.Fatal("the upgrade dropped a copied home setting")
	}
}
