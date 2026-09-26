package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMigration154MovesLastSeenStampsToUTC(t *testing.T) {
	stamps := []struct {
		id, stored, want string
	}{
		{"pacific", "2026-09-25T23:41:30.241015818-07:00", "2026-09-26T06:41:30.241015818Z"},
		{"berlin", "2026-09-26T08:41:30+02:00", "2026-09-26T06:41:30Z"},
		{"utc", "2026-09-26T06:41:30.5Z", "2026-09-26T06:41:30.5Z"},
		{"unseen", "", ""},
	}

	dbPath := filepath.Join(t.TempDir(), "migration-154.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB setup: %v", err)
	}
	for _, stamp := range stamps {
		if _, err := db.Exec(`INSERT INTO sessions (id, label, directory, state, state_since, state_updated_at, last_seen)
			VALUES (?, ?, '/tmp/shop', 'idle', '', '', ?)`, stamp.id, stamp.id, stamp.stored); err != nil {
			t.Fatalf("seed %s: %v", stamp.id, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version >= 154`); err != nil {
		t.Fatalf("rewind to schema 153: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close pre-154 database: %v", err)
	}

	migrated, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer migrated.Close()

	for _, stamp := range stamps {
		entry := migrated.SessionLedgerEntry(stamp.id)
		if entry == nil {
			t.Fatalf("session %s left the ledger", stamp.id)
		}
		if entry.LastSeen != stamp.want {
			t.Errorf("session %s last seen %q after the migration, want %q", stamp.id, entry.LastSeen, stamp.want)
		}
	}

	window := SessionLedgerQuery{
		Since: time.Date(2026, 9, 26, 6, 41, 0, 0, time.UTC),
		Until: time.Date(2026, 9, 26, 6, 42, 0, 0, time.UTC),
	}
	page, err := migrated.SessionLedger(window)
	if err != nil {
		t.Fatalf("ledger window: %v", err)
	}
	var inWindow []string
	for _, entry := range page.Entries {
		inWindow = append(inWindow, entry.ID)
	}
	if len(inWindow) != 3 {
		t.Errorf("sessions seen between 06:41 and 06:42 UTC = %q, want pacific, berlin and utc", inWindow)
	}
}
