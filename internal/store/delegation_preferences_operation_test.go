package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/delegationprefs"
)

func TestDelegationPreferencesMigrationUpgradesPreviousSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
		DROP TABLE delegation_preferences;
		ALTER TABLE delegation_operations DROP COLUMN resolved_preferences;
		DELETE FROM schema_migrations WHERE version = 138;
	`); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var count int
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'delegation_preferences'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("delegation_preferences table: count=%d err=%v", count, err)
	}
	if err := migrated.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('delegation_operations') WHERE name = 'resolved_preferences'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("resolved_preferences column: count=%d err=%v", count, err)
	}
}

func TestDelegationAcceptedSnapshotSurvivesSettingsEdits(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	cfg, err := s.SaveDelegationPreferences(delegationprefs.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(delegationprefs.Resolved{Revision: cfg.Revision, Instructions: "Original", Selection: delegationprefs.Selection{Harness: "codex"}})
	first, claimed, err := s.ClaimDelegationOperationWithPreferences("request-a", "op-a", "session-a", "", "", `{"role":"build"}`, string(raw), time.Now())
	if err != nil || !claimed {
		t.Fatalf("%+v %v", first, err)
	}
	cfg.Enabled = false
	if _, err = s.SaveDelegationPreferences(cfg); err != nil {
		t.Fatal(err)
	}
	retry, claimed, err := s.ClaimDelegationOperationWithPreferences("request-a", "op-b", "session-b", "", "", `{"role":"build"}`, string(raw), time.Now())
	if err != nil || claimed || retry.ResolvedPreferences != string(raw) || retry.Operation.OperationID != "op-a" {
		t.Fatalf("%+v %v", retry, err)
	}
	if _, _, err = s.ClaimDelegationOperationWithPreferences("request-new", "op-new", "session-new", "", "", `{}`, string(raw), time.Now()); !errors.Is(err, delegationprefs.ErrConflict) {
		t.Fatalf("stale new launch: %v", err)
	}
	if _, _, err = s.ClaimDelegationOperationWithPreferences("request-a", "op-c", "session-c", "", "", `{"role":"review"}`, string(raw), time.Now()); !errors.Is(err, ErrDelegationRequestConflict) {
		t.Fatalf("changed retry: %v", err)
	}
}
