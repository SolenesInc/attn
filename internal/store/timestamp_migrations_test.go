package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTimestampedMigrationsApplyInEitherArrivalOrder(t *testing.T) {
	older := migration{version: 20261005210000, desc: "older sessions column", sql: `ALTER TABLE sessions ADD COLUMN older_column TEXT NOT NULL DEFAULT 'older'`}
	newer := migration{version: 20261005210100, desc: "newer sessions column", sql: `ALTER TABLE sessions ADD COLUMN newer_column TEXT NOT NULL DEFAULT 'newer'`}
	for _, first := range []migration{older, newer} {
		t.Run(first.desc, func(t *testing.T) {
			original := timestampedMigrations
			defer func() { timestampedMigrations = original }()
			timestampedMigrations = nil
			registerMigration(first)
			path := filepath.Join(t.TempDir(), "attn.db")
			db, err := OpenDBAtSchemaVersion(path, first.version)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO settings(key, value) VALUES ('preserved', 'yes')`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			second := older
			if first.version == older.version {
				second = newer
			}
			registerMigration(second)
			current, err := OpenCurrent(path)
			if current != nil {
				current.Close()
			}
			var behind *SchemaBehindError
			if !errors.As(err, &behind) || !reflect.DeepEqual(behind.Missing, []int{second.version}) || !strings.Contains(err.Error(), fmt.Sprintf("%d %s", second.version, second.desc)) {
				t.Fatalf("OpenCurrent = %v; want missing %d %s", err, second.version, second.desc)
			}
			s, upgrade, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if upgrade.From != first.version || upgrade.To != newer.version {
				t.Fatalf("upgrade = %+v", upgrade)
			}
			if _, err := os.Stat(upgrade.BackupPath); err != nil {
				t.Fatalf("pre-migration backup: %v", err)
			}
			s.Close()
			db, err = OpenDBAtSchemaVersion(path, newer.version)
			if err != nil {
				t.Fatal(err)
			}
			var value string
			if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'preserved'`).Scan(&value); err != nil || value != "yes" {
				t.Fatalf("setting = %q (%v)", value, err)
			}
			for _, column := range []string{"older_column", "newer_column"} {
				var count int
				if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('sessions') WHERE name = ?`, column).Scan(&count); err != nil || count != 1 {
					t.Fatalf("column %s: %d (%v)", column, count, err)
				}
			}
			db.Close()
			current, err = OpenCurrent(path)
			if err != nil {
				t.Fatal(err)
			}
			current.Close()
		})
	}
}

func TestLegacyMigrationRowGapsDoNotReplayAppliedMigrations(t *testing.T) {
	for _, through := range []int{100, 169, 171} {
		t.Run(fmt.Sprint(through), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attn.db")
			db, err := OpenDBAtSchemaVersion(path, through)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version IN (1, 99)`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			s, _, err := Open(path)
			if err != nil {
				t.Fatalf("upgrading legacy database with gaps: %v", err)
			}
			s.Close()
			db, err = OpenDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var gaps int
			if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version IN (1, 99)`).Scan(&gaps); err != nil || gaps != 0 {
				t.Fatalf("old migrations replayed: %d (%v)", gaps, err)
			}
		})
	}
}

func TestFailedTimestampedUpgradeRollsBackAllPendingWork(t *testing.T) {
	original := timestampedMigrations
	defer func() { timestampedMigrations = original }()
	timestampedMigrations = nil
	path := filepath.Join(t.TempDir(), "attn.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	registerMigration(migration{version: 20261005210200, desc: "first pending column", sql: `ALTER TABLE sessions ADD COLUMN rollback_column TEXT`})
	registerMigration(migration{version: 20261005210300, desc: "failing pending column", sql: `ALTER TABLE absent_table ADD COLUMN failure TEXT`})
	s, upgrade, err := Open(path)
	if s != nil {
		s.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "20261005210300 (failing pending column)") {
		t.Fatalf("Open = %v", err)
	}
	if _, err := os.Stat(upgrade.BackupPath); err != nil {
		t.Fatalf("backup: %v", err)
	}
	db, err = sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var columns, applied int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('sessions') WHERE name = 'rollback_column'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version >= 20261005210200`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if columns != 0 || applied != 0 {
		t.Fatalf("failed upgrade left %d columns and %d migration rows", columns, applied)
	}
}

func TestAnOlderBuildAcceptsUnknownNewerMigrations(t *testing.T) {
	original := timestampedMigrations
	defer func() { timestampedMigrations = original }()
	timestampedMigrations = nil
	known := migration{version: 20261005210400, desc: "known column", sql: `ALTER TABLE sessions ADD COLUMN known_column TEXT`}
	registerMigration(known)
	registerMigration(migration{version: 20271005210400, desc: "future data migration", apply: func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO settings(key, value) VALUES ('future_backfill', 'preserved')`)
		return err
	}})
	path := filepath.Join(t.TempDir(), "attn.db")
	s, _, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	timestampedMigrations = []migration{known}
	s, err = OpenCurrent(path)
	if err != nil {
		t.Fatalf("older build refused newer database: %v", err)
	}
	s.Close()
	s, upgrade, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if upgrade.BackupPath != "" {
		t.Fatalf("no pending work wrote backup %s", upgrade.BackupPath)
	}
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'future_backfill'`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("Go migration value = %q (%v)", value, err)
	}
}

func TestAnOlderMicrosecondMigrationAppliesAfterANewerOne(t *testing.T) {
	original := timestampedMigrations
	defer func() { timestampedMigrations = original }()
	timestampedMigrations = nil
	stamp := time.Date(2026, 10, 6, 12, 34, 56, 123456000, time.UTC)
	older := migration{version: int(stamp.UnixMicro()), desc: "older microsecond", sql: `ALTER TABLE sessions ADD COLUMN microsecond_older TEXT`}
	newer := migration{version: int(stamp.Add(time.Microsecond).UnixMicro()), desc: "newer microsecond", sql: `ALTER TABLE sessions ADD COLUMN microsecond_newer TEXT`}
	registerMigration(newer)
	path := filepath.Join(t.TempDir(), "attn.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	registerMigration(older)
	current, err := OpenCurrent(path)
	if current != nil {
		current.Close()
	}
	var behind *SchemaBehindError
	if !errors.As(err, &behind) || !reflect.DeepEqual(behind.Missing, []int{older.version}) {
		t.Fatalf("missing older id = %v", err)
	}
	s, upgrade, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if upgrade.From != newer.version || upgrade.To != newer.version {
		t.Fatalf("upgrade = %+v", upgrade)
	}
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var columns int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('sessions') WHERE name IN ('microsecond_older','microsecond_newer')`).Scan(&columns); err != nil || columns != 2 {
		t.Fatalf("microsecond columns = %d (%v)", columns, err)
	}
}
