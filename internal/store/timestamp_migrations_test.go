package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
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

func databaseWithHotJournal(t *testing.T, path string) string {
	t.Helper()
	u := &url.URL{Scheme: "file", Path: path}
	db := sql.OpenDB(&sqliteConnector{driver: &sqlite3.SQLiteDriver{}, dsn: u.String()})
	var err error
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA journal_mode=DELETE`,
		`PRAGMA cache_size=1`,
		`PRAGMA cache_spill=1`,
		`INSERT INTO settings(key, value) VALUES ('crash_recovery', 'committed')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var pageSize, pageCount int
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`, LatestSchemaVersion()+2); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE settings SET value=zeroblob(?) WHERE key='crash_recovery'`, pageSize*pageCount); err != nil {
		t.Fatal(err)
	}
	crashed := filepath.Join(t.TempDir(), "attn.db")
	for _, suffix := range []string{"", "-journal"} {
		data, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(crashed+suffix, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	u.Path = crashed
	u.RawQuery = "mode=ro"
	inspection, err := sql.Open("sqlite3", u.String())
	if err != nil {
		t.Fatal(err)
	}
	var count int
	err = inspection.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	inspection.Close()
	var sqliteError sqlite3.Error
	if !errors.As(err, &sqliteError) || sqliteError.ExtendedCode != sqlite3.ErrReadonlyRollback {
		t.Fatalf("crash fixture is not awaiting rollback recovery: %v", err)
	}
	return crashed
}

func TestSupportedDatabasesRecoverHotJournalsBeforeOpening(t *testing.T) {
	for _, version := range []int{lastLegacyVersion, LatestSchemaVersion()} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "attn.db")
			db, err := OpenDBAtSchemaVersion(path, version)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			path = databaseWithHotJournal(t, path)
			var s *Store
			if version == LatestSchemaVersion() {
				s, err = OpenCurrent(path)
			} else {
				s, _, err = Open(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if value := s.GetSetting("crash_recovery"); value != "committed" {
				t.Fatalf("recovered setting = %q, want committed", value)
			}
		})
	}
}

func TestAnOlderBuildRefusesNewerDatabasesWithoutChangingThem(t *testing.T) {
	for _, placement := range []string{"above_latest", "below_latest"} {
		for _, journal := range []string{"DELETE", "WAL", "closed_WAL", "hot_DELETE"} {
			for _, missingKnown := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/missing_known=%t", placement, journal, missingKnown), func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "attn ?#.db")
					db, err := OpenDB(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}
					u := &url.URL{Scheme: "file", Path: path}
					db = sql.OpenDB(&sqliteConnector{driver: &sqlite3.SQLiteDriver{}, dsn: u.String()})
					db.SetMaxOpenConns(1)
					t.Cleanup(func() { db.Close() })
					mode := "DELETE"
					if strings.HasSuffix(journal, "WAL") {
						mode = "WAL"
					}
					if _, err := db.Exec("PRAGMA journal_mode=" + mode); err != nil {
						t.Fatal(err)
					}
					unknown := LatestSchemaVersion() + 1
					if placement == "below_latest" {
						if err := db.QueryRow(`SELECT MAX(applied.version - 1)
						FROM schema_migrations applied
						LEFT JOIN schema_migrations previous ON previous.version = applied.version - 1
						WHERE previous.version IS NULL AND applied.version > ?`, lastLegacyVersion+1).Scan(&unknown); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`, unknown); err != nil {
						t.Fatal(err)
					}
					if missingKnown {
						if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = ?`, LatestSchemaVersion()); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := db.Exec(`INSERT INTO settings(key, value) VALUES ('future_backfill', 'preserved')`); err != nil {
						t.Fatal(err)
					}
					recorded, err := GetSchemaVersion(db)
					if err != nil {
						t.Fatal(err)
					}
					if journal != "WAL" {
						if err := db.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if journal == "hot_DELETE" {
						path = databaseWithHotJournal(t, path)
					}
					before := make(map[string][]byte)
					for _, suffix := range []string{"", "-wal", "-journal"} {
						data, err := os.ReadFile(path + suffix)
						if os.IsNotExist(err) {
							continue
						}
						if err != nil {
							t.Fatal(err)
						}
						before[path+suffix] = data
					}
					entries, err := os.ReadDir(filepath.Dir(path))
					if err != nil {
						t.Fatal(err)
					}
					want := fmt.Sprintf("the database at %s contains migration %d, which this attn build does not support (newest database schema version: %d; newest build schema version: %d); install an attn build supporting this database's migrations, or restore a backup taken before the upgrade (`attn db restore`)", path, unknown, recorded, LatestSchemaVersion())
					for _, opener := range []struct {
						name string
						open func() error
					}{
						{"OpenCurrent", func() error {
							s, err := OpenCurrent(path)
							if s != nil {
								s.Close()
							}
							return err
						}},
						{"Open", func() error {
							s, _, err := Open(path)
							if s != nil {
								s.Close()
							}
							return err
						}},
						{"OpenDB", func() error {
							d, err := OpenDB(path)
							if d != nil {
								d.Close()
							}
							return err
						}},
						{"OpenDBAtSchemaVersion", func() error {
							d, err := OpenDBAtSchemaVersion(path, LatestSchemaVersion())
							if d != nil {
								d.Close()
							}
							return err
						}},
					} {
						t.Run(opener.name, func(t *testing.T) {
							if err := opener.open(); err == nil || err.Error() != want {
								t.Fatalf("open = %v, want %s", err, want)
							}
							for file, data := range before {
								after, err := os.ReadFile(file)
								if err != nil || !bytes.Equal(data, after) {
									t.Fatalf("refused open changed %s: %v", file, err)
								}
							}
							afterEntries, err := os.ReadDir(filepath.Dir(path))
							if err != nil {
								t.Fatal(err)
							}
							if journal == "closed_WAL" {
								kept := afterEntries[:0]
								for _, entry := range afterEntries {
									if entry.Name() == filepath.Base(path)+"-wal" {
										data, err := os.ReadFile(path + "-wal")
										if err != nil || len(data) != 0 {
											t.Fatalf("refused open wrote WAL content: %v", err)
										}
										continue
									}
									if entry.Name() != filepath.Base(path)+"-shm" {
										kept = append(kept, entry)
									}
								}
								afterEntries = kept
							}
							if !reflect.DeepEqual(entries, afterEntries) {
								t.Fatalf("refused open changed directory entries: %v", err)
							}
						})
					}
				})
			}
		}
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
