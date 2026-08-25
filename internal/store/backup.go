package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Rotating backup names: attn-<UTC YYYYMMDD-HHMMSS>.db. Pre-migration snapshots
// use a different prefix so the rotation prune never counts or removes them.
const (
	backupNamePrefix = "attn-"
	backupNameLayout = "20060102-150405"
	backupNameSuffix = ".db"
)

const backupPremigrationKeep = 5

const premigrationNamePrefix = "attn-premigration-"

const premigrationTimestampLen = len(backupNameLayout)

// BackupNow is safe while the daemon serves traffic: VACUUM INTO reads a
// consistent snapshot without blocking writers.
func (s *Store) BackupNow(dir string, keep int) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("backup: store has no open database")
	}
	if !s.durable {
		return "", fmt.Errorf("backup: store is not durably backed (in-memory fallback), skipping backup")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("backup: create dir %s: %w", dir, err)
	}

	name := backupNamePrefix + time.Now().UTC().Format(backupNameLayout) + backupNameSuffix
	target := filepath.Join(dir, name)

	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("backup: target %s already exists", target)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("backup: stat target %s: %w", target, err)
	}

	if _, err := s.db.Exec("VACUUM INTO ?", target); err != nil {
		return "", fmt.Errorf("backup: vacuum into %s: %w", target, err)
	}

	if err := pruneBackups(dir, keep); err != nil {
		return target, fmt.Errorf("backup: wrote %s but prune failed: %w", target, err)
	}

	return target, nil
}

// Lexical sort on the fixed-width timestamp is chronological.
func pruneBackups(dir string, keep int) error {
	if keep < 0 {
		keep = 0
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if IsRotatingBackupName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if len(names) <= keep {
		return nil
	}

	toRemove := names[:len(names)-keep]
	var firstErr error
	for _, name := range toRemove {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %s: %w", name, err)
		}
	}
	return firstErr
}

func backupPreMigration(db *sql.DB, dbPath string, version int) (string, error) {
	dir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create dir %s: %w", dir, err)
	}

	name := fmt.Sprintf("attn-premigration-%d-%s%s", version, time.Now().UTC().Format(backupNameLayout), backupNameSuffix)
	target := filepath.Join(dir, name)

	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("target %s already exists", target)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat target %s: %w", target, err)
	}

	if _, err := db.Exec("VACUUM INTO ?", target); err != nil {
		return "", fmt.Errorf("vacuum into %s: %w", target, err)
	}

	if err := pruneBackupPremigration(dir, backupPremigrationKeep); err != nil {
		return target, fmt.Errorf("wrote %s but premigration prune failed: %w", target, err)
	}

	return target, nil
}

// Ordered by the trailing timestamp: the embedded schema version varies in digit
// count, so a whole-filename sort is not chronological.
func pruneBackupPremigration(dir string, keep int) error {
	if keep < 0 {
		keep = 0
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}

	type snapshot struct {
		name string
		ts   string
	}
	var snapshots []snapshot
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, premigrationNamePrefix) || !strings.HasSuffix(name, backupNameSuffix) {
			continue
		}
		stem := strings.TrimSuffix(name, backupNameSuffix)
		if len(stem) < premigrationTimestampLen {
			continue
		}
		ts := stem[len(stem)-premigrationTimestampLen:]
		if _, err := time.Parse(backupNameLayout, ts); err != nil {
			continue
		}
		snapshots = append(snapshots, snapshot{name: name, ts: ts})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].ts < snapshots[j].ts
	})

	if len(snapshots) <= keep {
		return nil
	}

	toRemove := snapshots[:len(snapshots)-keep]
	var firstErr error
	for _, s := range toRemove {
		if err := os.Remove(filepath.Join(dir, s.name)); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %s: %w", s.name, err)
		}
	}
	return firstErr
}

func IsRotatingBackupName(name string) bool {
	if !strings.HasPrefix(name, backupNamePrefix) || !strings.HasSuffix(name, backupNameSuffix) {
		return false
	}
	stem := strings.TrimSuffix(strings.TrimPrefix(name, backupNamePrefix), backupNameSuffix)
	if _, err := time.Parse(backupNameLayout, stem); err != nil {
		return false
	}
	return true
}
