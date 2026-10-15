package store

import (
	"path/filepath"
	"testing"
)

func TestMigration168PreservesInboxHistoryAndWatchAddresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox-upgrade.db")
	db, err := openDBAtVersion(path, 167)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
 INSERT INTO agent_mailbox_items(id,recipient_session_id,kind,source_id,coalesce_key,hint,prompt,created_at,notified_at,read_at,bell_name) VALUES
 ('queued','day-a','peer_message','queued','','','', '2026-09-12T12:00:00Z','','',''),
 ('rung','day-a','garden_seed','s-work','s-work','note','', '2026-09-12T12:00:01Z','2026-09-12T12:01:00Z','','seed activity'),
 ('read','day-b','maintenance_prompt','','restart','restart','Please close your day.', '2026-09-12T12:00:02Z','2026-09-12T12:01:02Z','2026-09-12T12:02:00Z','');
 INSERT INTO pull_request_watches VALUES('day-a','github.com/repo#1','readiness','','2026-09-12T12:00:00Z','{}','2026-09-12T12:01:00Z','','',0);
 INSERT INTO jobs(id,kind,state,scheduled_at,created_at,updated_at) VALUES('old-reconcile','reconcile','pending','','',''),('old-recovery','recover_legacy_closed_work','pending','','','');
 INSERT INTO tasks(id,kind,subject,state,next_attempt_at,created_at,updated_at) VALUES('old-reconcile','reconcile','unlinked','pending','','',''),('old-recovery','recover_legacy_closed_work','unlinked','pending','','','');
 `); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tickets(id,title,status,created_at,updated_at) VALUES('unlinked','Historical work','working','2026-09-12T12:00:00Z','2026-09-12T12:00:00Z'); INSERT INTO ticket_activity(ticket_id,kind,author,comment,created_at) VALUES('unlinked','comment','day-a','In progress','2026-09-12T12:01:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(db, path); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		id, address             string
		attempts                int
		attempted, readBy, text string
	}{
		{"queued", "session:day-a", 0, "", "", ""},
		{"rung", "session:day-a", 1, "2026-09-12T12:01:00Z", "", ""},
		{"read", "session:day-b", 0, "", "day-b", "Please close your day."},
	} {
		var address, attempted, readBy, text string
		var attempts int
		if err := db.QueryRow("SELECT address,attempts,attempted_at,read_by,text FROM inbox_items WHERE id=?", want.id).Scan(&address, &attempts, &attempted, &readBy, &text); err != nil {
			t.Fatal(err)
		}
		if address != want.address || attempts != want.attempts || attempted != want.attempted || readBy != want.readBy || text != want.text {
			t.Fatalf("%s: address=%s attempts=%d attempted=%s readBy=%s text=%q", want.id, address, attempts, attempted, readBy, text)
		}
	}
	var address string
	if err := db.QueryRow("SELECT watcher FROM pull_request_watches").Scan(&address); err != nil || address != "session:day-a" {
		t.Fatalf("watch address=%q err=%v", address, err)
	}
	for _, table := range []string{"jobs", "tasks"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE kind IN ('reconcile','recover_legacy_closed_work')").Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired %s count=%d err=%v", table, count, err)
		}
	}
	var outstanding string
	if err := db.QueryRow("SELECT outstanding_at FROM inbox_delivery WHERE address='session:day-a'").Scan(&outstanding); err != nil || outstanding != "2026-09-12T12:01:00Z" {
		t.Fatalf("migrated outstanding=%q err=%v", outstanding, err)
	}
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != LatestSchemaVersion() {
		t.Fatalf("MAX(version)=%d err=%v", version, err)
	}
	if _, err := db.Exec("SELECT * FROM agent_mailbox_items"); err == nil {
		t.Fatal("old mailbox survived")
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND (name LIKE 'ticket%' OR name LIKE 'legacy_ticket_%' OR name='automation_ticket_occurrence_events')`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("retired tables=%d", remaining)
	}
}
