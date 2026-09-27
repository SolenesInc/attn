package daemon_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

const (
	backupRotationKeep     = 12
	backupPremigrationKeep = 5
)

func TestTheDaemonBacksUpOnStartKeepingTheNewestRotationAndPreMigrationSnapshots(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "backups")
	var backups string
	var rotating, premigration []string
	legacyRecoveryUpgrade(t, func(dir string) {
		backups = filepath.Join(dir, "backups")
		for day := 1; day <= backupRotationKeep+1; day++ {
			rotating = append(rotating, plantBackup(t, backups, fmt.Sprintf("attn-199912%02d-000000.db", day)))
		}
		for day := 1; day <= backupPremigrationKeep+2; day++ {
			premigration = append(premigration, plantBackup(t, backups, fmt.Sprintf("attn-premigration-9-199912%02d-000000.db", day)))
		}
	}, func(t *testing.T, w *world) {
		w.advance(0)
		taken := backupsNamed(t, backups, "attn-2000")
		if len(taken) != 1 {
			t.Fatalf("after starting, the daemon has taken backups %v, want one", taken)
		}
		assertUsableDatabase(t, filepath.Join(backups, taken[0]))
		if got := backupsNamed(t, backups, "attn-1999"); !slices.Equal(got, rotating[2:]) {
			t.Errorf("the rotation kept %v, want the %d newest older backups %v beside the one taken", got, backupRotationKeep-1, rotating[2:])
		}
		if got := backupsNamed(t, backups, "attn-premigration-"); !slices.Equal(got, premigration[2:]) {
			t.Errorf("the pre-migration snapshots kept are %v, want the %d newest %v", got, backupPremigrationKeep, premigration[2:])
		}
	})
}

func plantBackup(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("an older snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

func backupsNamed(t *testing.T, dir, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".db") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func assertUsableDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var check string
	var tables int
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Errorf("the backup %s does not open as a sound database: %q, %v", path, check, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil || tables == 0 {
		t.Errorf("the backup %s holds %d tables (%v), want the daemon's database", path, tables, err)
	}
}
