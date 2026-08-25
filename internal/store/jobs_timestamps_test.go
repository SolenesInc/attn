package store

import (
	"path/filepath"
	"testing"
	"time"
)

// These stamps are TEXT columns, so encoding defines "before": under RFC3339Nano
// "…:00Z" sorts ABOVE every stamp in its own second ('Z' is 0x5A, above '.' and digits).

var raggedJobOffsets = []struct {
	id     string
	offset time.Duration
}{
	{"j0", 0},
	{"j1234", 123400 * time.Microsecond},
	{"j12345", 123450 * time.Microsecond},
	{"j5", 500 * time.Millisecond},
}

func jobBase() time.Time { return time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) }

func jobIDs(recs []JobRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}

func chronologicalJobIDs() []string {
	out := make([]string, 0, len(raggedJobOffsets))
	for _, r := range raggedJobOffsets {
		out = append(out, r.id)
	}
	return out
}

func storeWithRaggedJobs(t *testing.T) *Store {
	t.Helper()
	s := New()
	for _, r := range raggedJobOffsets {
		if err := s.UpsertJob(newJobRecord(r.id, "compact_context", jobBase().Add(r.offset))); err != nil {
			t.Fatalf("upsert %s: %v", r.id, err)
		}
	}
	return s
}

func TestAJobScheduledOnAWholeSecondIsClaimableAtThatSecond(t *testing.T) {
	s := New()
	at := jobBase()
	if err := s.UpsertJob(newJobRecord("whole", "compact_context", at)); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for _, now := range []time.Time{at, at.Add(time.Millisecond), at.Add(500 * time.Millisecond)} {
		got, err := s.EligibleJobs(now, 10)
		if err != nil {
			t.Fatalf("eligible jobs at %v: %v", now, err)
		}
		if len(got) != 1 {
			t.Fatalf("a job scheduled at %s was not claimable at %s: got %v",
				at.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), jobIDs(got))
		}
	}
}

func TestEligibleJobsComeBackInScheduledOrderWithinASecond(t *testing.T) {
	s := storeWithRaggedJobs(t)

	got, err := s.EligibleJobs(jobBase().Add(time.Second), 10)
	if err != nil {
		t.Fatalf("eligible jobs: %v", err)
	}
	if want := chronologicalJobIDs(); !sameOrder(jobIDs(got), want) {
		t.Fatalf("eligible jobs came back as %v, want %v", jobIDs(got), want)
	}
}

func TestListJobsIsNewestUpdatedFirstWithinASecond(t *testing.T) {
	s := storeWithRaggedJobs(t)

	got, err := s.ListJobs()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if want := reversed(chronologicalJobIDs()); !sameOrder(jobIDs(got), want) {
		t.Fatalf("list jobs came back as %v, want %v", jobIDs(got), want)
	}
}

func TestTrimDoneJobsDeletesByTimeWithinASecond(t *testing.T) {
	s := New()
	for _, r := range raggedJobOffsets {
		rec := newJobRecord(r.id, "compact_context", jobBase().Add(r.offset))
		rec.State = "done"
		if err := s.UpsertJob(rec); err != nil {
			t.Fatalf("upsert %s: %v", r.id, err)
		}
	}

	n, err := s.TrimDoneJobs(jobBase().Add(200 * time.Millisecond))
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if n != 3 {
		t.Fatalf("trimmed %d jobs, want the 3 updated before the cutoff", n)
	}
	left, err := s.ListJobs()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if want := []string{"j5"}; !sameOrder(jobIDs(left), want) {
		t.Fatalf("trim left %v, want %v", jobIDs(left), want)
	}
}

func TestNotificationsListNewestFirstWithinASecond(t *testing.T) {
	s := New()
	for _, r := range raggedJobOffsets {
		if _, err := s.AddNotification(
			NotificationRecord{Kind: "task_failed", Title: r.id}, jobBase().Add(r.offset)); err != nil {
			t.Fatalf("add notification %s: %v", r.id, err)
		}
	}

	got, err := s.ListNotifications()
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	titles := make([]string, 0, len(got))
	for _, n := range got {
		titles = append(titles, n.Title)
	}
	if want := reversed(chronologicalJobIDs()); !sameOrder(titles, want) {
		t.Fatalf("notifications came back as %v, want %v", titles, want)
	}
}

func TestMigration94RewritesJobAndNotificationStampsThatDoNotSort(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	defer s.Close()

	for _, r := range raggedJobOffsets {
		if err := s.UpsertJob(newJobRecord(r.id, "compact_context", jobBase().Add(r.offset))); err != nil {
			t.Fatalf("upsert %s: %v", r.id, err)
		}
		if _, err := s.AddNotification(
			NotificationRecord{Kind: "task_failed", Title: r.id}, jobBase().Add(r.offset)); err != nil {
			t.Fatalf("add notification %s: %v", r.id, err)
		}
	}

	// The planted unreadable value must be left alone rather than turned into year
	// 1, and read_at stays '' — an unread sentinel must survive too.
	for _, r := range raggedJobOffsets {
		old := jobBase().Add(r.offset).Format(time.RFC3339Nano)
		if _, err := s.db.Exec(
			`UPDATE jobs SET scheduled_at = ?, created_at = ?, updated_at = ? WHERE id = ?`,
			old, old, old, r.id); err != nil {
			t.Fatalf("plant old job stamp for %s: %v", r.id, err)
		}
		if _, err := s.db.Exec(
			`UPDATE notifications SET created_at = ? WHERE title = ?`, old, r.id); err != nil {
			t.Fatalf("plant old notification stamp for %s: %v", r.id, err)
		}
	}
	if _, err := s.db.Exec(
		`UPDATE jobs SET created_at = 'not a timestamp' WHERE id = ?`, "j5"); err != nil {
		t.Fatalf("plant unreadable stamp: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 94`); err != nil {
		t.Fatalf("unrecord migration 94: %v", err)
	}

	// Sanity: the planted state is the broken one, so a pass here would mean the
	// test proves nothing.
	if got, err := s.EligibleJobs(jobBase(), 10); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Fatalf("the planted stamps already claim correctly (%v); this test would pass without the migration", jobIDs(got))
	}

	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	assertMigration94Applied(t, s)

	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 94`); err != nil {
		t.Fatalf("unrecord migration 94 again: %v", err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatalf("re-run migrateDB: %v", err)
	}
	assertMigration94Applied(t, s)
}

func assertMigration94Applied(t *testing.T, s *Store) {
	t.Helper()
	got, err := s.EligibleJobs(jobBase(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"j0"}; !sameOrder(jobIDs(got), want) {
		t.Fatalf("after migration 94 the jobs claimable at the whole second are %v, want %v", jobIDs(got), want)
	}

	notes, err := s.ListNotifications()
	if err != nil {
		t.Fatal(err)
	}
	titles := make([]string, 0, len(notes))
	for _, n := range notes {
		titles = append(titles, n.Title)
	}
	if want := reversed(chronologicalJobIDs()); !sameOrder(titles, want) {
		t.Fatalf("after migration 94 the notifications list as %v, want %v", titles, want)
	}
	for _, n := range notes {
		if !n.ReadAt.IsZero() {
			t.Fatalf("notification %q came back read; read_at must stay the unread sentinel", n.Title)
		}
	}
	var unreadable string
	if err := s.db.QueryRow(`SELECT created_at FROM jobs WHERE id = ?`, "j5").Scan(&unreadable); err != nil {
		t.Fatal(err)
	}
	if unreadable != "not a timestamp" {
		t.Fatalf("the unreadable stamp became %q; it must be left as it was", unreadable)
	}
}
