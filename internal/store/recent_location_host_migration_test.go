package store

import (
	"path/filepath"
	"testing"
)

func TestRecentLocationHostMigrationKeepsExistingRecentsOnTheHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recents.db")
	db, err := OpenDBAtSchemaVersion(path, 1793426028786836)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO recent_locations(path,last_seen,use_count) VALUES('/work/shop','2026-10-30T00:00:00Z',3)`); err != nil {
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
	var host, lastSeen string
	var useCount int
	var repoRoot *string
	if err = upgraded.QueryRow(`SELECT host, last_seen, use_count, repo_root FROM recent_locations WHERE path='/work/shop'`).Scan(&host, &lastSeen, &useCount, &repoRoot); err != nil {
		t.Fatal(err)
	}
	if host != "" || lastSeen != "2026-10-30T00:00:00Z" || useCount != 3 || repoRoot != nil {
		t.Fatalf("migrated recent = host %q last_seen %q use_count %d repo_root %v; want the row kept on the home", host, lastSeen, useCount, repoRoot)
	}
	if _, err = upgraded.Exec(`INSERT INTO recent_locations(host,path,last_seen,use_count) VALUES('outpost-1','/work/shop','2026-10-31T00:00:00Z',1)`); err != nil {
		t.Fatalf("the same folder on an outpost is its own recent: %v", err)
	}
}
