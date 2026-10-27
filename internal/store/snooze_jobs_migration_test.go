package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReplacingSnoozeJobsPreservesDeadlinesAndOtherJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snooze-upgrade.db")
	db, err := OpenDBAtSchemaVersion(path, 1792106066149768)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO sessions(id,label,directory,state_since,state_updated_at,last_seen,turn_snoozed_until)
		VALUES('sleeping','Sleep','/tmp/shop','','','','2026-10-19T12:00:00Z');
		INSERT INTO jobs(id,kind,state,scheduled_at,created_at,updated_at) VALUES
		('old-wake','session_snooze_wake','pending','','',''),('kept-job','session_activity','pending','','','');
		INSERT INTO tasks(id,kind,subject,state,next_attempt_at,created_at,updated_at) VALUES
		('old-wake','session_snooze_wake','sleeping','pending','','',''),('kept-task','session_activity','sleeping','pending','','','');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.TurnStamps("sleeping").SnoozedUntil; got.Format(time.RFC3339) != "2026-10-19T12:00:00Z" {
		t.Fatalf("snooze deadline after upgrade = %s", got)
	}
	for _, table := range []string{"jobs", "tasks"} {
		var retired, kept int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table + " WHERE kind = 'session_snooze_wake'").Scan(&retired); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("SELECT count(*) FROM " + table + " WHERE kind = 'session_activity'").Scan(&kept); err != nil {
			t.Fatal(err)
		}
		if retired != 0 || kept != 1 {
			t.Fatalf("%s after upgrade: retired=%d kept=%d", table, retired, kept)
		}
	}
}
