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
