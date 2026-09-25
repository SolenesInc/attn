package store

import (
	"path/filepath"
	"testing"
	"time"
)

func seedApps(t *testing.T, s *Store, now time.Time) (older, newer AppVersion) {
	t.Helper()

	older, created, err := s.CommitAppVersion(AppVersion{
		AppName:      "approval-gate",
		ContentHash:  "sha256:1111",
		Declaration:  `{"name":"approval-gate","subscribe":[{"events":["delegation.*"]}]}`,
		ArtifactPath: "apps/approval-gate/1111/bundle.js",
	}, now)
	if err != nil {
		t.Fatalf("commit older version: %v", err)
	}
	if !created {
		t.Fatal("commit older version reported reuse, want a new row")
	}
	newer, created, err = s.CommitAppVersion(AppVersion{
		AppName:      "approval-gate",
		ContentHash:  "sha256:2222",
		Declaration:  `{"name":"approval-gate","subscribe":[{"events":["delegation.*","session.state.changed"]}]}`,
		ArtifactPath: "apps/approval-gate/2222/bundle.js",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("commit newer version: %v", err)
	}
	if !created {
		t.Fatal("commit newer version reported reuse, want a new row")
	}
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName:      "standup-digest",
		ContentHash:  "sha256:3333",
		Declaration:  `{"name":"standup-digest","subscribe":[{"events":["ticket.*"]}]}`,
		ArtifactPath: "apps/standup-digest/3333/bundle.js",
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("commit other app version: %v", err)
	}
	return older, newer
}

func TestApps_InvocationsListNewestFirstWithinOneSecond(t *testing.T) {
	s := New()
	base := time.Date(2026, 8, 9, 10, 30, 0, 0, time.UTC)
	_, newer := seedApps(t, s, base)

	for i, offset := range []time.Duration{0, 250 * time.Millisecond, 900 * time.Millisecond} {
		status, failure := "ok", ""
		if i == 2 {
			status, failure = "error", "TypeError: cannot read property 'id' of undefined"
		}
		if _, err := s.AppendAppInvocation(AppInvocation{
			AppName: "approval-gate", VersionID: newer.ID, EventSeq: int64(100 + i),
			EventName: "delegation.requested", EventSubject: "del-" + string(rune('a'+i)),
			Handler: "delegation.*", Status: status, Error: failure,
			Duration: time.Duration(i+1) * 7 * time.Millisecond, StartedAt: base.Add(offset),
		}); err != nil {
			t.Fatalf("append invocation %d: %v", i, err)
		}
	}
	if _, err := s.AppendAppInvocation(AppInvocation{
		AppName: "standup-digest", VersionID: 3, EventSeq: 999, Status: "ok", StartedAt: base,
	}); err != nil {
		t.Fatalf("append other app invocation: %v", err)
	}

	got, err := s.ListAppInvocations("approval-gate", 10)
	if err != nil {
		t.Fatalf("list invocations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("invocations = %d, want 3", len(got))
	}
	if got[0].EventSeq != 102 || got[2].EventSeq != 100 {
		t.Fatalf("order = %d,%d,%d, want 102,101,100", got[0].EventSeq, got[1].EventSeq, got[2].EventSeq)
	}
	if got[0].Status != "error" || got[0].Error == "" {
		t.Fatalf("failure detail lost: %+v", got[0])
	}
	if got[0].Duration != 21*time.Millisecond {
		t.Fatalf("duration = %v, want 21ms", got[0].Duration)
	}
	if !got[0].StartedAt.Equal(base.Add(900 * time.Millisecond)) {
		t.Fatalf("started_at = %v, want %v", got[0].StartedAt, base.Add(900*time.Millisecond))
	}
	if limited, err := s.ListAppInvocations("approval-gate", 2); err != nil || len(limited) != 2 {
		t.Fatalf("limit ignored: %d (%v)", len(limited), err)
	}
}

func TestApps_MigrationCarriesTheRecordedPredecessorIntoTheChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newSeededStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	older, newer := seedApps(t, s, now)
	s.Close()

	db, err := openSeededDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE apps ADD COLUMN previous_version_id INTEGER",
		"ALTER TABLE apps DROP COLUMN serving_step_id",
		"DROP TABLE app_serving_steps",
		"DELETE FROM schema_migrations WHERE version >= 105",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("rewinding with %q: %v", stmt, err)
		}
	}
	if _, err := db.Exec(
		"UPDATE apps SET previous_version_id = ? WHERE name = 'approval-gate'", older.ID); err != nil {
		t.Fatalf("seeding the recorded predecessor: %v", err)
	}
	db.Close()

	s, err = newSeededStore(path)
	if err != nil {
		t.Fatalf("reopen after rewind: %v", err)
	}
	defer s.Close()
	app, ok, err := s.GetApp("approval-gate")
	if err != nil || !ok {
		t.Fatalf("get app after migration: %v ok=%t", err, ok)
	}
	if app.CurrentVersionID != newer.ID {
		t.Fatalf("the migration moved the current version to %d, want %d", app.CurrentVersionID, newer.ID)
	}
	if app.PreviousServingVersionID != older.ID {
		t.Fatalf("one step back after the migration = %d, want the recorded %d", app.PreviousServingVersionID, older.ID)
	}
	if err := s.StepAppVersionBack("approval-gate", older.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("walking the carried chain: %v", err)
	}
	if app, _, err := s.GetApp("approval-gate"); err != nil || app.PreviousServingVersionID != 0 {
		t.Fatalf("the carried chain has more below the oldest version: %+v (%v)", app, err)
	}
	if err := s.StepAppVersionBack("approval-gate", older.ID, now.Add(2*time.Hour)); err == nil {
		t.Fatal("walking past the bottom of a carried chain was accepted")
	}

	other, _, err := s.GetApp("standup-digest")
	if err != nil {
		t.Fatalf("get the single-version app: %v", err)
	}
	if other.CurrentVersionID == 0 {
		t.Fatal("the migration lost the current version pointer")
	}
	if other.PreviousServingVersionID != 0 {
		t.Fatalf("the migration invented a predecessor: %d", other.PreviousServingVersionID)
	}
}
