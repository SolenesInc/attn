package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

// These stamp columns are TEXT, so every SQL comparison is a text comparison; these
// offsets encode in an order that is not their own ('Z' is 0x5A, above '.' and digits).

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

// The guard asks `turn_opened_at <= turn_settled_at` in text, so a settle in the same
// second as its open reads as still-open and the session drops out of the queue.
func TestATurnSettledInTheSecondItOpenedInCanReopen(t *testing.T) {
	cases := []struct {
		name            string
		opened, settled time.Duration
	}{
		{"opened on a whole second", 0, 500 * time.Millisecond},
		{"settled fraction extends the opened one", 123400 * time.Microsecond, 123450 * time.Microsecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTurnStore(t)
			addTurnSession(t, s, "s1", "working")

			if !s.OpenTurnIfClosed("s1", turnBase().Add(c.opened)) {
				t.Fatalf("the first turn did not open")
			}
			if !s.SettleTurn("s1", turnBase().Add(c.settled)) {
				t.Fatalf("settle failed")
			}
			if stamps := s.TurnStamps("s1"); !stamps.SettledAt.After(stamps.OpenedAt) {
				t.Fatalf("fixture is wrong: settled %s is not after opened %s",
					stamps.SettledAt, stamps.OpenedAt)
			}

			if !s.OpenTurnIfClosed("s1", turnBase().Add(c.settled+time.Millisecond)) {
				t.Fatalf("no turn opened after a settle in the same second; the session is silently out of the queue")
			}
		})
	}
}

// turn_snoozed_until is matched for equality, and a whole second is where the two
// encodings disagree ("…:00Z" against "…:00.000000000Z").
func TestASnoozeWrittenInTheOldEncodingIsStillWakeable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
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

// The schedule cursor is deliberately not the subject: its upsert carries no
// WHERE, so it advances whatever the encoding does and proves nothing.
func TestAReviewRequestCursorAdvancesWithinASecond(t *testing.T) {
	s := newTurnStore(t)
	def, err := s.UpsertAutomationDefinition("reviews", "Reviews", `{"id":"reviews"}`, turnBase())
	if err != nil {
		t.Fatalf("upsert definition: %v", err)
	}

	first := turnBase()
	second := turnBase().Add(500 * time.Millisecond)
	for _, at := range []time.Time{first, second} {
		if _, err := s.ReconcileAutomationReviewRequests(
			def.ID, "github.com", []string{"github.com/owner/repo#1"}, at); err != nil {
			t.Fatalf("reconcile at %s: %v", at, err)
		}
	}

	var raw string
	if err := s.db.QueryRow(
		`SELECT observed_at FROM automation_provider_cursors
		  WHERE definition_id = ? AND provider = 'github_review_requested' AND scope = 'github.com'`,
		def.ID).Scan(&raw); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	got, err := docstore.ParseTime(raw)
	if err != nil {
		t.Fatalf("parse cursor %q: %v", raw, err)
	}
	if !got.Equal(second) {
		t.Fatalf("cursor is at %s, want the later observation %s", got, second)
	}
}

func TestPendingDelegationOperationsAreInClaimOrderWithinASecond(t *testing.T) {
	s := newTurnStore(t)

	for _, r := range raggedOffsets {
		if _, _, err := s.ClaimDelegationOperation(
			r.id, "op-"+r.id, "sess-"+r.id, "chief", "", `{}`, turnBase().Add(r.offset)); err != nil {
			t.Fatalf("claim %s: %v", r.id, err)
		}
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
		t.Fatalf("pending delegations came back as %v, want %v", ids, want)
	}
}

func TestWorkflowRunsAreNewestFirstWithinASecond(t *testing.T) {
	s := newTurnStore(t)

	for _, r := range raggedOffsets {
		at := turnBase().Add(r.offset).Format(time.RFC3339Nano)
		if err := s.UpsertWorkflowRun(&WorkflowRunRow{
			RunID: r.id, ScriptPath: "s.js", ScriptHash: "h", Status: "running",
			CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("upsert run %s: %v", r.id, err)
		}
	}

	runs, err := s.ListWorkflowRuns("")
	if err != nil {
		t.Fatalf("list workflow runs: %v", err)
	}
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.RunID)
	}
	if want := reversed(chronologicalRaggedIDs()); !sameOrder(ids, want) {
		t.Fatalf("workflow runs came back as %v, want %v", ids, want)
	}
}

func TestMigration95RewritesTurnCursorAndListingStampsThatDoNotSort(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
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

	// An unsnoozed session holds '' as a sentinel, not a stamp that failed to parse.
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
