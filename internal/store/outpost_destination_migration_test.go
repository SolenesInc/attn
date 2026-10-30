package store

import (
	"path/filepath"
	"testing"
)

func TestOutpostDestinationMigrationPreservesLegacyAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outposts.db")
	db, err := OpenDBAtSchemaVersion(path, 1793328356112305)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO outposts(id,name,address,added_at) VALUES('legacy','Legacy','example.com:7777','2026-10-30T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var destination string
	if err = upgraded.QueryRow(`SELECT destination FROM outposts WHERE id='legacy'`).Scan(&destination); err != nil || destination != "example.com:7777" {
		t.Fatalf("legacy destination %q: %v", destination, err)
	}
}
