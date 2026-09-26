package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func TestDatabaseBackupPruningWaitsForLegacyRecovery(t *testing.T) {
	t.Setenv("ATTN_INSTANCE", "")
	dataRoot := t.TempDir()
	makeRecoveryHome(t, dataRoot)
	s, err := store.NewWithDB(filepath.Join(dataRoot, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	backupDir := filepath.Join(dataRoot, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= backupKeep; i++ {
		name := fmt.Sprintf("attn-202608%02d-010101.db", i+1)
		if err := os.WriteFile(filepath.Join(backupDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d := &Daemon{store: s, dataRoot: dataRoot}
	if _, _, err := s.BeginLegacyTicketRecovery(store.LegacyTicketRecoveryVersion, nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	d.pruneEligibleDatabaseBackups()
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != backupKeep+1 {
		t.Fatalf("running recovery left %d backups, want %d", len(entries), backupKeep+1)
	}

	if _, err := s.FinishLegacyTicketRecovery(store.LegacyTicketRecoveryVersion,
		store.LegacyTicketRecoverySucceeded, struct{}{}, "", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	d.pruneEligibleDatabaseBackups()
	entries, err = os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != backupKeep {
		t.Fatalf("finished recovery left %d backups, want %d", len(entries), backupKeep)
	}
}
