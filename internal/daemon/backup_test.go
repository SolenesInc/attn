package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func TestPerformDatabaseBackup_SurfacesLastBackupAt(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := store.NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB error: %v", err)
	}
	defer s.Close()

	d := &Daemon{store: s, dataRoot: t.TempDir()}

	before := time.Now().UTC()
	d.performDatabaseBackup()

	settings := d.settingsWithAgentAvailability()
	raw, ok := settings[SettingDBLastBackupAt]
	if !ok {
		t.Fatalf("settings missing %s after successful backup: %v", SettingDBLastBackupAt, settings)
	}
	str, ok := raw.(string)
	if !ok {
		t.Fatalf("%s is not a string: %v (%T)", SettingDBLastBackupAt, raw, raw)
	}
	parsed, err := time.Parse(time.RFC3339, str)
	if err != nil {
		t.Fatalf("%s = %q is not parseable RFC3339: %v", SettingDBLastBackupAt, str, err)
	}
	if parsed.Before(before.Add(-time.Second)) || parsed.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("%s = %v is outside the expected window around %v", SettingDBLastBackupAt, parsed, before)
	}
}

func TestPerformDatabaseBackup_FailedBackupLeavesKeyAbsent(t *testing.T) {
	s := store.New()
	defer s.Close()

	d := &Daemon{store: s, dataRoot: t.TempDir()}
	d.performDatabaseBackup()

	settings := d.settingsWithAgentAvailability()
	if raw, ok := settings[SettingDBLastBackupAt]; ok {
		t.Fatalf("%s should be absent after a failed backup, got %v", SettingDBLastBackupAt, raw)
	}
}
