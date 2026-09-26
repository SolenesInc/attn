package store

import (
	"path/filepath"
	"testing"
	"time"
)

var raggedOffsets = []struct {
	id     string
	offset time.Duration
}{
	{"r0", 0},
	{"r1234", 123400 * time.Microsecond},
	{"r12345", 123450 * time.Microsecond},
	{"r5", 500 * time.Millisecond},
}

func turnBase() time.Time { return time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) }

func chronologicalRaggedIDs() []string {
	out := make([]string, 0, len(raggedOffsets))
	for _, r := range raggedOffsets {
		out = append(out, r.id)
	}
	return out
}

func TestASnoozeWrittenInTheOldEncodingIsStillWakeable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()
	addTurnSession(t, s, "s1", "working")

	until := turnBase().Add(time.Hour)
	if _, err := s.db.Exec(`UPDATE sessions SET turn_snoozed_until = ? WHERE id = 's1'`,
		until.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("plant old snooze stamp: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 95`); err != nil {
		t.Fatalf("unrecord migration 95: %v", err)
	}

	if s.WakeTurnAt("s1", until) {
		t.Fatalf("the planted stamp already matches; this test would pass without the migration")
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	if got := s.SnoozedSessions()["s1"]; !got.Equal(until) {
		t.Fatalf("stored deadline reads back as %s, want %s", got, until)
	}
	if !s.WakeTurnAt("s1", until) {
		t.Fatalf("after migration 95 the fired timer still could not cash its deadline")
	}
}

func TestMigration95RewritesTurnCursorAndListingStampsThatDoNotSort(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := newSeededStore(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	addTurnSession(t, s, "s1", "working")
	for _, r := range raggedOffsets {
		if _, _, err := s.ClaimDelegationOperation(
			r.id, "op-"+r.id, "sess-"+r.id, "chief", "", `{}`, turnBase().Add(r.offset)); err != nil {
			t.Fatalf("claim %s: %v", r.id, err)
		}
	}

	if _, err := s.db.Exec(
		`UPDATE sessions SET turn_opened_at = ?, turn_settled_at = ? WHERE id = 's1'`,
		turnBase().Format(time.RFC3339Nano),
		turnBase().Add(500*time.Millisecond).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("plant old turn stamps: %v", err)
	}
	for _, r := range raggedOffsets {
		old := turnBase().Add(r.offset).Format(time.RFC3339Nano)
		if _, err := s.db.Exec(
			`UPDATE delegation_operations SET created_at = ? WHERE request_id = ?`, old, r.id); err != nil {
			t.Fatalf("plant old delegation stamp for %s: %v", r.id, err)
		}
	}
	if _, err := s.db.Exec(
		`UPDATE delegation_operations SET updated_at = 'not a timestamp' WHERE request_id = ?`, "r5"); err != nil {
		t.Fatalf("plant unreadable stamp: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 95`); err != nil {
		t.Fatalf("unrecord migration 95: %v", err)
	}

	if s.OpenTurnIfClosed("s1", turnBase().Add(time.Second)) {
		t.Fatalf("the planted turn stamps already reopen correctly; this test would pass without the migration")
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	assertMigration95Applied(t, s)

	before := stampDigest(t, s)
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 95`); err != nil {
		t.Fatalf("unrecord migration 95 again: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("re-run migrateDB: %v", err)
	}
	if after := stampDigest(t, s); after != before {
		t.Fatalf("a second run changed the stamps:\n%s\nto\n%s", before, after)
	}
}

func assertMigration95Applied(t *testing.T, s *Store) {
	t.Helper()

	if !s.OpenTurnIfClosed("s1", turnBase().Add(time.Second)) {
		t.Fatalf("after migration 95 a settled turn still did not reopen")
	}

	got, err := s.PendingDelegationOperations()
	if err != nil {
		t.Fatalf("pending delegation operations: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, rec := range got {
		ids = append(ids, rec.Operation.RequestID)
	}
	if want := chronologicalRaggedIDs(); !sameOrder(ids, want) {
		t.Fatalf("after migration 95 the delegations came back as %v, want %v", ids, want)
	}

	var unreadable string
	if err := s.db.QueryRow(
		`SELECT updated_at FROM delegation_operations WHERE request_id = 'r5'`).Scan(&unreadable); err != nil {
		t.Fatal(err)
	}
	if unreadable != "not a timestamp" {
		t.Fatalf("the unreadable stamp became %q; it must be left as it was", unreadable)
	}

	var snoozed string
	if err := s.db.QueryRow(`SELECT turn_snoozed_until FROM sessions WHERE id = 's1'`).Scan(&snoozed); err != nil {
		t.Fatal(err)
	}
	if snoozed != "" {
		t.Fatalf("turn_snoozed_until became %q; the unsnoozed sentinel must be left as it is", snoozed)
	}
}

func stampDigest(t *testing.T, s *Store) string {
	t.Helper()
	var digest string
	if err := s.db.QueryRow(`
		SELECT (SELECT group_concat(turn_opened_at || '|' || turn_settled_at || '|' || turn_snoozed_until, ';')
		          FROM (SELECT * FROM sessions ORDER BY id))
		    || '#' ||
		       (SELECT group_concat(created_at || '|' || updated_at, ';')
		          FROM (SELECT * FROM delegation_operations ORDER BY request_id))
	`).Scan(&digest); err != nil {
		t.Fatalf("stamp digest: %v", err)
	}
	return digest
}
