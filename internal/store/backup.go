package store

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	backupNamePrefix = "attn-"
	backupNameLayout = "20060102-150405"
	backupNameSuffix = ".db"
)

const backupPremigrationKeep = 5

const premigrationNamePrefix = "attn-premigration-"

const premigrationTimestampLen = len(backupNameLayout)

func (s *Store) BackupNow(ctx context.Context, dir string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("backup: store has no open database")
	}
	if !s.durable {
		return "", fmt.Errorf("backup: store is not durably backed (in-memory fallback), skipping backup")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("backup: create dir %s: %w", dir, err)
	}

	taken := time.Now().UTC().Format(backupNameLayout)
	var target string
	for n := 1; ; n++ {
		target = filepath.Join(dir, rotatingBackupName(taken, n))
		_, err := os.Stat(target)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("backup: stat target %s: %w", target, err)
		}
	}

	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		_ = os.Remove(target)
		return "", fmt.Errorf("backup: vacuum into %s: %w", target, err)
	}

	return target, nil
}

func PruneBackups(dir string, keep int, protected map[string]struct{}) error {
	if keep < 0 {
		keep = 0
	}

	names, err := RotatingBackups(dir)
	if err != nil {
		return err
	}

	if len(names) <= keep {
		return nil
	}

	toRemove := names[:len(names)-keep]
	var firstErr error
	for _, name := range toRemove {
		path := filepath.Clean(filepath.Join(dir, name))
		if _, ok := protected[path]; ok {
			continue
		}
		if err := os.Remove(path); err != nil && firstErr == nil {
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

	stamp := time.Now().UTC().Format(backupNameLayout)
	target := filepath.Join(dir, fmt.Sprintf("%s%d-%s%s", premigrationNamePrefix, version, stamp, backupNameSuffix))
	for attempt := 2; ; attempt++ {
		_, err := os.Stat(target)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("stat target %s: %w", target, err)
		}
		target = filepath.Join(dir, fmt.Sprintf("%s%d.%d-%s%s", premigrationNamePrefix, version, attempt, stamp, backupNameSuffix))
	}

	if _, err := db.Exec("VACUUM INTO ?", target); err != nil {
		return "", fmt.Errorf("vacuum into %s: %w", target, err)
	}

	return target, nil
}

func PrunePremigrationBackups(dir string, keep int, protected map[string]struct{}) error {
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
		path := filepath.Clean(filepath.Join(dir, s.name))
		if _, ok := protected[path]; ok {
			continue
		}
		if err := os.Remove(path); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %s: %w", s.name, err)
		}
	}
	return firstErr
}

type rotatingBackup struct {
	taken string
	n     int
}

func rotatingBackupName(taken string, n int) string {
	if n == 1 {
		return backupNamePrefix + taken + backupNameSuffix
	}
	return fmt.Sprintf("%s%s-%d%s", backupNamePrefix, taken, n, backupNameSuffix)
}

func parseRotatingBackupName(name string) (rotatingBackup, bool) {
	stem, ok := strings.CutPrefix(name, backupNamePrefix)
	if !ok {
		return rotatingBackup{}, false
	}
	if stem, ok = strings.CutSuffix(stem, backupNameSuffix); !ok || len(stem) < len(backupNameLayout) {
		return rotatingBackup{}, false
	}
	taken, rest := stem[:len(backupNameLayout)], stem[len(backupNameLayout):]
	if _, err := time.Parse(backupNameLayout, taken); err != nil {
		return rotatingBackup{}, false
	}
	n := 1
	if rest != "" {
		digits, ok := strings.CutPrefix(rest, "-")
		parsed, err := strconv.Atoi(digits)
		if !ok || err != nil || parsed < 2 || strconv.Itoa(parsed) != digits {
			return rotatingBackup{}, false
		}
		n = parsed
	}
	return rotatingBackup{taken: taken, n: n}, true
}

func RotatingBackups(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var backups []rotatingBackup
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if b, ok := parseRotatingBackupName(e.Name()); ok {
			backups = append(backups, b)
		}
	}
	slices.SortFunc(backups, func(a, b rotatingBackup) int {
		return cmp.Or(strings.Compare(a.taken, b.taken), cmp.Compare(a.n, b.n))
	})
	names := make([]string, len(backups))
	for i, b := range backups {
		names[i] = rotatingBackupName(b.taken, b.n)
	}
	return names, nil
}

func PremigrationBackupKeep() int { return backupPremigrationKeep }

func BackupDirForDatabase(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "backups")
}
