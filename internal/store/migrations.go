package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

const lastLegacyVersion = 171

//go:embed migrations
var migrationFiles embed.FS

var migrationFilename = regexp.MustCompile(`^([0-9]{14}|[0-9]{16})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)

var timestampedMigrations = loadSQLMigrations()

type migration struct {
	version int
	desc    string
	sql     string
	apply   func(*sql.Tx) error
}

func (m migration) run(tx *sql.Tx) error {
	if m.apply != nil {
		return m.apply(tx)
	}
	_, err := tx.Exec(m.sql)
	return err
}

func loadSQLMigrations() []migration {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		panic(err)
	}
	var result []migration
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		m := migrationFromFilename(entry.Name())
		data, err := migrationFiles.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			panic(err)
		}
		m.sql = string(data)
		result = appendMigration(result, m)
	}
	return result
}

func migrationFromFilename(filename string) migration {
	parts := migrationFilename.FindStringSubmatch(filename)
	if parts == nil {
		panic(fmt.Sprintf("malformed migration filename %q; use <unix-microseconds>_slug.sql", filename))
	}
	version, err := parseMigrationID(parts[1])
	if err != nil {
		panic(fmt.Sprintf("malformed migration timestamp in %q: %v", filename, err))
	}

	return migration{version: version, desc: strings.ReplaceAll(parts[2], "_", " ")}
}

func parseMigrationID(raw string) (int, error) {
	version, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if version > 1<<53-1 {
		return 0, fmt.Errorf("exact JSON integer limit is %d, asked for %d", int64(1<<53-1), version)
	}
	switch len(raw) {
	case 14:
		if _, err := time.Parse("20060102150405", raw); err != nil {
			return 0, err
		}
	case 16:
	default:
		return 0, fmt.Errorf("use a Unix-microsecond id or an existing 14-digit UTC-second id, got %q", raw)
	}
	return version, nil
}

func registerMigration(m migration) {
	timestampedMigrations = appendMigration(timestampedMigrations, m)
}

func appendMigration(all []migration, m migration) []migration {
	if m.version <= lastLegacyVersion {
		panic(fmt.Sprintf("migration %d must be after frozen legacy version %d", m.version, lastLegacyVersion))
	}
	if _, err := parseMigrationID(strconv.Itoa(m.version)); err != nil {
		panic(fmt.Sprintf("malformed migration timestamp %d: %v", m.version, err))
	}
	if (m.sql == "") == (m.apply == nil) {
		panic(fmt.Sprintf("migration %d must have either SQL or a Go function", m.version))
	}
	for _, known := range all {
		if known.version == m.version {
			panic(fmt.Sprintf("duplicate migration id %d (%s and %s)", m.version, known.desc, m.desc))
		}
	}
	return append(all, m)
}

func allMigrations() []migration {
	all := append(append([]migration(nil), migrations...), timestampedMigrations...)
	sort.Slice(all, func(i, j int) bool { return all[i].version < all[j].version })
	return all
}

func LatestSchemaVersion() int {
	all := allMigrations()
	if len(all) == 0 {
		return 0
	}
	return all[len(all)-1].version
}

func refuseUnsupportedSchema(dbPath string) error {
	if dbPath == ":memory:" {
		return nil
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("checking the database at %s: %w", dbPath, err)
	}
	absolute, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	u := &url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return err
	}
	history, err := readMigrationHistory(db)
	db.Close()
	var sqliteError sqlite3.Error
	if errors.As(err, &sqliteError) && sqliteError.ExtendedCode == sqlite3.ErrReadonlyRollback {
		history, err = readRecoveredMigrationHistory(dbPath)
	}
	if err != nil {
		return fmt.Errorf("reading the schema version of %s: %w", dbPath, err)
	}
	latest := LatestSchemaVersion()
	known := make(map[int]bool)
	for _, m := range allMigrations() {
		known[m.version] = true
	}
	var unsupported int
	for version := range history.applied {
		if version > latest || (version > lastLegacyVersion && !known[version]) {
			unsupported = max(unsupported, version)
		}
	}
	if unsupported != 0 {
		return fmt.Errorf("the database at %s contains migration %d, which this attn build does not support (newest database schema version: %d; newest build schema version: %d); install an attn build supporting this database's migrations, or restore a backup taken before the upgrade (`attn db restore`)", dbPath, unsupported, history.recorded, latest)
	}
	return nil
}

func readRecoveredMigrationHistory(dbPath string) (migrationHistory, error) {
	var history migrationHistory
	dir, err := os.MkdirTemp("", "attn-schema-check-")
	if err != nil {
		return history, err
	}
	defer os.RemoveAll(dir)
	recoveredPath := filepath.Join(dir, "attn.db")
	for _, suffix := range []string{"", "-journal"} {
		source, err := os.Open(dbPath + suffix)
		if err != nil {
			return history, err
		}
		target, err := os.OpenFile(recoveredPath+suffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			source.Close()
			return history, err
		}
		_, err = io.Copy(target, source)
		if err := errors.Join(err, target.Close(), source.Close()); err != nil {
			return history, err
		}
	}
	u := &url.URL{Scheme: "file", Path: recoveredPath, RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return history, err
	}
	defer db.Close()
	return readMigrationHistory(db)
}

type migrationHistory struct {
	applied        map[int]bool
	recorded       int
	legacyRecorded int
	legacy         int
}

func readMigrationHistory(db *sql.DB) (migrationHistory, error) {
	h := migrationHistory{applied: make(map[int]bool)}
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&tables); err != nil {
		return h, err
	}
	if tables != 0 {
		rows, err := db.Query(`SELECT version FROM schema_migrations`)
		if err != nil {
			return h, err
		}
		for rows.Next() {
			var version int
			if err := rows.Scan(&version); err != nil {
				rows.Close()
				return h, err
			}
			h.applied[version] = true
			if version > h.recorded {
				h.recorded = version
			}
			if version <= lastLegacyVersion && version > h.legacyRecorded {
				h.legacyRecorded = version
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return h, err
		}
	}
	var err error
	h.legacy, err = legacySchemaVersion(db, h.legacyRecorded)
	return h, err
}

func (h migrationHistory) pending(through int) []migration {
	var pending []migration
	for _, m := range allMigrations() {
		if m.version > through {
			break
		}
		if m.version <= lastLegacyVersion {
			if m.version <= h.legacy {
				continue
			}
		} else if h.applied[m.version] {
			continue
		}
		pending = append(pending, m)
	}
	return pending
}

type SchemaBehindError struct {
	DatabasePath string
	Missing      []int
	descriptions []string
}

func (e *SchemaBehindError) Error() string {
	return fmt.Sprintf("the database at %s is missing %d schema migrations this attn needs (%s); only the daemon upgrades it, so start the daemon (`attn daemon ensure`) and retry", e.DatabasePath, len(e.Missing), strings.Join(e.descriptions, ", "))
}

func openCurrentDB(dbPath string) (*sql.DB, *tableWrites, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil, fmt.Errorf("opening the database at %s: %w", dbPath, err)
	}
	db, writes, err := openSQLite(dbPath)
	if err != nil {
		return nil, nil, err
	}
	history, err := readMigrationHistory(db)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("reading the schema version of %s: %w", dbPath, err)
	}
	if pending := history.pending(LatestSchemaVersion()); len(pending) != 0 {
		db.Close()
		behind := &SchemaBehindError{DatabasePath: dbPath}
		for _, m := range pending {
			behind.Missing = append(behind.Missing, m.version)
			behind.descriptions = append(behind.descriptions, fmt.Sprintf("%d %s", m.version, m.desc))
		}
		return nil, nil, behind
	}
	return db, writes, nil
}

func OpenDBAtSchemaVersion(dbPath string, version int) (*sql.DB, error) {
	db, _, err := openSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	history, err := readMigrationHistory(db)
	if err == nil {
		err = applyPendingMigrations(db, history, version)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func upgradeSchema(db *sql.DB, dbPath string) (SchemaUpgrade, error) {
	upgrade := SchemaUpgrade{DatabasePath: dbPath, To: LatestSchemaVersion()}
	history, err := readMigrationHistory(db)
	if err != nil {
		return upgrade, fmt.Errorf("getting schema version: %w", err)
	}
	upgrade.From = history.recorded
	if len(history.pending(upgrade.To)) == 0 {
		return upgrade, nil
	}
	backupVersion := max(history.recorded, history.legacy)
	if backupVersion > 0 && dbPath != "" && dbPath != ":memory:" {
		backup, err := backupPreMigration(db, dbPath, backupVersion)
		if err != nil {
			return upgrade, fmt.Errorf("backing up %s before upgrading schema v%d to v%d: %w", dbPath, backupVersion, upgrade.To, err)
		}
		upgrade.BackupPath = backup
		log.Printf("[store] pre-migration backup written to %s (schema v%d -> v%d)", backup, backupVersion, upgrade.To)
	}
	if err := applyPendingMigrations(db, history, upgrade.To); err != nil {
		return upgrade, err
	}
	return upgrade, nil
}

func applyPendingMigrations(db *sql.DB, history migrationHistory, through int) error {
	pending := history.pending(through)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("starting the schema upgrade transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(baseSchema); err != nil {
		return fmt.Errorf("creating the base schema: %w", err)
	}
	if err := recordLegacySchemaVersions(tx, history.legacyRecorded, history.legacy); err != nil {
		return err
	}
	for _, m := range pending {
		if err := m.run(tx); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.desc, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))", m.version); err != nil {
			return fmt.Errorf("recording migration %d: %w", m.version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing the schema upgrade: %w", err)
	}
	return nil
}
