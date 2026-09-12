package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
)

func TestMigration146AddsGuardianAndPreservesItOnReplay(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`ALTER TABLE automode_config DROP COLUMN guardian; DELETE FROM schema_migrations WHERE version >= 146;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatal(err)
	}
	config, err := s.GetAutoModeConfig()
	if err != nil || config.Guardian != (automode.GuardianSelection{}) {
		t.Fatalf("migrated config: %+v, %v", config, err)
	}
	want := automode.GuardianSelection{Provider: "provider", Model: "review", Effort: "high"}
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{Guardian: &want}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version >= 146`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(s.db, dbPath); err != nil {
		t.Fatal(err)
	}
	config, err = s.GetAutoModeConfig()
	if err != nil || config.Guardian != want {
		t.Fatalf("replayed migration: %+v, %v", config.Guardian, err)
	}
}

func TestGuardianConfigPersistsAndUnrelatedPolicyEditsPreserveIt(t *testing.T) {
	s := New()
	defer s.Close()
	want := automode.GuardianSelection{Provider: "provider", Model: "review/model", Effort: "high"}
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{Guardian: &want}, time.Now()); err != nil {
		t.Fatal(err)
	}
	policy := automode.PolicyUntrusted
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{ApprovalPolicy: &policy}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Guardian != want {
		t.Fatalf("guardian = %+v, want %+v", got.Guardian, want)
	}
	invalid := automode.GuardianSelection{Provider: "broken"}
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{Guardian: &invalid}, time.Now()); err == nil {
		t.Fatal("accepted incomplete selection")
	}
	got, err = s.GetAutoModeConfig()
	if err != nil || got.Guardian != want {
		t.Fatalf("invalid edit changed config: %+v, %v", got.Guardian, err)
	}
	reset := automode.GuardianSelection{}
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{Guardian: &reset}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetAutoModeConfig()
	if err != nil || got.Guardian != reset {
		t.Fatalf("reset: %+v, %v", got.Guardian, err)
	}
}
