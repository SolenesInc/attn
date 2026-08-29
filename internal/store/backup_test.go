package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestBackupNow_ProducesValidSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	s.Add(&protocol.Session{
		ID:         "session-1",
		Label:      "one",
		Directory:  "/tmp/one",
		State:      protocol.SessionStateWorking,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	})
	s.Add(&protocol.Session{
		ID:         "session-2",
		Label:      "two",
		Directory:  "/tmp/two",
		State:      protocol.SessionStateIdle,
		StateSince: protocol.TimestampNow().String(),
		LastSeen:   protocol.TimestampNow().String(),
	})

	backupDir := filepath.Join(t.TempDir(), "backups")
	backupPath, err := s.BackupNow(backupDir)
	if err != nil {
		t.Fatalf("BackupNow error: %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if filepath.Dir(backupPath) != backupDir {
		t.Fatalf("backup path %s not under %s", backupPath, backupDir)
	}

	wantVersion, err := GetSchemaVersion(s.db)
	if err != nil {
		t.Fatalf("GetSchemaVersion(source): %v", err)
	}

	backupDB, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro", backupPath))
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backupDB.Close()

	gotVersion, err := GetSchemaVersion(backupDB)
	if err != nil {
		t.Fatalf("GetSchemaVersion(backup): %v", err)
	}
	if gotVersion != wantVersion {
		t.Fatalf("backup schema_version = %d, want %d", gotVersion, wantVersion)
	}

	var count int
	if err := backupDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions in backup: %v", err)
	}
	if count != 2 {
		t.Fatalf("backup sessions count = %d, want 2", count)
	}

	var label string
	if err := backupDB.QueryRow(`SELECT label FROM sessions WHERE id = ?`, "session-1").Scan(&label); err != nil {
		t.Fatalf("read session-1 from backup: %v", err)
	}
	if label != "one" {
		t.Fatalf("session-1 label in backup = %q, want %q", label, "one")
	}
}

func TestBackupNow_TargetAlreadyExists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	backupDir := t.TempDir()
	// Seed both the current-second and next-second target names: a second boundary crossed
	// between this test's time.Now() and BackupNow's would pass vacuously.
	now := time.Now().UTC()
	for _, ts := range []time.Time{now, now.Add(time.Second)} {
		name := backupNamePrefix + ts.Format(backupNameLayout) + backupNameSuffix
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("occupied"), 0644); err != nil {
			t.Fatalf("seed colliding file: %v", err)
		}
	}

	if _, err := s.BackupNow(backupDir); err == nil {
		t.Fatal("expected BackupNow to fail on an existing target, got nil error")
	}
}

func TestBackupNow_Rotation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	backupDir := t.TempDir()
	const keep = 3

	var existing []string
	for i := 0; i < keep+3; i++ {
		name := fmt.Sprintf("%s202601%02d-000000%s", backupNamePrefix, i+1, backupNameSuffix)
		path := filepath.Join(backupDir, name)
		if err := os.WriteFile(path, []byte("fake"), 0644); err != nil {
			t.Fatalf("seed fake backup %s: %v", name, err)
		}
		existing = append(existing, name)
	}

	premigrationName := "attn-premigration-42-20260101-000000.db"
	if err := os.WriteFile(filepath.Join(backupDir, premigrationName), []byte("fake"), 0644); err != nil {
		t.Fatalf("seed premigration file: %v", err)
	}

	newPath, err := s.BackupNow(backupDir)
	if err != nil {
		t.Fatalf("BackupNow error: %v", err)
	}
	if err := PruneBackups(backupDir, keep, nil); err != nil {
		t.Fatalf("PruneBackups error: %v", err)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	var rotating []string
	sawPremigration := false
	sawNew := false
	for _, e := range entries {
		if e.Name() == premigrationName {
			sawPremigration = true
			continue
		}
		if e.Name() == filepath.Base(newPath) {
			sawNew = true
		}
		if IsRotatingBackupName(e.Name()) {
			rotating = append(rotating, e.Name())
		}
	}

	if !sawPremigration {
		t.Fatal("premigration snapshot was pruned; it must be exempt")
	}
	if !sawNew {
		t.Fatal("newly-written backup missing after rotation")
	}
	if len(rotating) != keep {
		t.Fatalf("rotating backups after prune = %d, want %d (%v)", len(rotating), keep, rotating)
	}

	oldestTwo := existing[:2]
	for _, name := range oldestTwo {
		if _, err := os.Stat(filepath.Join(backupDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected oldest backup %s to be pruned, stat err = %v", name, err)
		}
	}
	newest := existing[len(existing)-1]
	if _, err := os.Stat(filepath.Join(backupDir, newest)); err != nil {
		t.Fatalf("expected newest pre-seeded backup %s to survive prune: %v", newest, err)
	}
}

func TestPruneBackupsKeepsProtectedSnapshotsBeyondTheRotation(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for day := 1; day <= 5; day++ {
		path := filepath.Join(dir, fmt.Sprintf("attn-202601%02d-000000.db", day))
		if err := os.WriteFile(path, []byte("snapshot"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	protected := map[string]struct{}{paths[0]: {}}
	if err := PruneBackups(dir, 2, protected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("protected oldest snapshot was removed: %v", err)
	}
	for _, path := range paths[3:] {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("newest rotating snapshot was removed: %v", err)
		}
	}
}

func TestBackupNow_RefusesNonDurableStore(t *testing.T) {
	s := New()
	defer s.Close()

	backupDir := t.TempDir()
	if _, err := s.BackupNow(backupDir); err == nil {
		t.Fatal("expected BackupNow to refuse a non-durable (in-memory fallback) store, got nil error")
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("BackupNow wrote to backups dir despite refusing: %v", entries)
	}
}

func TestMigrateDB_PreMigrationBackup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
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

func TestBackupPreMigration_CapsSnapshots(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatalf("mkdir backups dir: %v", err)
	}

	// Differing digit widths (9 vs 10 vs 100), so a whole-filename sort misorders them
	// relative to a timestamp sort.
	versions := []int{9, 10, 100, 11, 12, 13, 14}
	var seeded []string
	for i, v := range versions {
		ts := fmt.Sprintf("202601%02d-000000", i+1)
		name := fmt.Sprintf("attn-premigration-%d-%s.db", v, ts)
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte("fake"), 0644); err != nil {
			t.Fatalf("seed premigration file %s: %v", name, err)
		}
		seeded = append(seeded, name)
	}

	rotatingName := backupNamePrefix + "20260101-000000" + backupNameSuffix
	if err := os.WriteFile(filepath.Join(backupDir, rotatingName), []byte("fake"), 0644); err != nil {
		t.Fatalf("seed rotating file: %v", err)
	}

	latest := latestSchemaVersion()
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, latest); err != nil {
		t.Fatalf("unrecord latest migration: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB error: %v", err)
	}
	beforePrune, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	var beforePremigration int
	for _, entry := range beforePrune {
		if IsPremigrationBackupName(entry.Name()) {
			beforePremigration++
		}
	}
	if beforePremigration != len(seeded)+1 {
		t.Fatalf("migration pruned before the recovery fence: got %d snapshots, want %d", beforePremigration, len(seeded)+1)
	}
	if err := PrunePremigrationBackups(backupDir, backupPremigrationKeep, nil); err != nil {
		t.Fatalf("PrunePremigrationBackups error: %v", err)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}

	var premigration []string
	sawRotating := false
	for _, e := range entries {
		if e.Name() == rotatingName {
			sawRotating = true
			continue
		}
		if strings.HasPrefix(e.Name(), premigrationNamePrefix) {
			premigration = append(premigration, e.Name())
		}
	}

	if !sawRotating {
		t.Fatal("rotating backup was removed by premigration prune; it must be untouched")
	}

	if len(premigration) != backupPremigrationKeep {
		t.Fatalf("premigration snapshots after prune = %d, want %d (%v)", len(premigration), backupPremigrationKeep, premigration)
	}

	for _, name := range seeded[:3] {
		if _, err := os.Stat(filepath.Join(backupDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected oldest premigration file %s to be pruned, stat err = %v", name, err)
		}
	}
	for _, name := range seeded[3:] {
		if _, err := os.Stat(filepath.Join(backupDir, name)); err != nil {
			t.Fatalf("expected premigration file %s to survive prune: %v", name, err)
		}
	}
}
