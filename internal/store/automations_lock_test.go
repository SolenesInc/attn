package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestScheduledAutomationClaimWaitsForConcurrentWriter(t *testing.T) {
	s, err := NewWithDB(filepath.Join(t.TempDir(), "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`INSERT INTO jobs(id,kind,state,scheduled_at,created_at,updated_at) VALUES('j','k','queued','','','')`); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		// The busy wait is native SQLite real time, out of synctest's reach; a
		// held write is the only way to stage the collision, so the hold is a sleep.
		time.Sleep(200 * time.Millisecond)
		if err := writer.Commit(); err != nil {
			t.Error(err)
		}
		close(released)
	}()
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", TicketID: "auto-run-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	_, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, ids)
	<-released
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
}
