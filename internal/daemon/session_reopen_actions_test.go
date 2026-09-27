package daemon

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

var errSpawnRefusedInThisTest = errors.New("the pty backend refuses to spawn in this test")

func reopenDaemonWithBackend(t *testing.T, d *Daemon) *fakeSpawnBackend {
	t.Helper()
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	t.Cleanup(d.stopEventBus)
	return backend
}

func TestAReopenComesBackUnplacedInItsProfileUnderItsOwnID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	writeCodexRolloutFixture(t, "conv-unplaced")
	closeReopenSession(t, d, reopenSession{
		ID: "unplaced", Directory: t.TempDir(), Agent: "codex", Resume: "conv-unplaced",
	})
	before := spawnCount(backend)

	outcome, err := d.reopenSession("unplaced", "", "", profileDestination{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if outcome.SessionID != "unplaced" || outcome.ProfileID != defaultProfileID(t, d.store) {
		t.Errorf("outcome = %+v, want the same id back in the default profile", outcome)
	}
	if spawn := resumeSpawnForSession(t, backend, "unplaced", before); spawn.ID != "unplaced" {
		t.Errorf("spawned %q, want the session's own id", spawn.ID)
	}
	if _, placed, err := d.store.SessionPlacement("unplaced"); err != nil || placed {
		t.Errorf("placed=%v err=%v, want the reopened agent unplaced", placed, err)
	}
}

func TestReopeningIntoADeletedProfileNeedsALiveDestination(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	writeCodexRolloutFixture(t, "conv-orphan")
	work := createTestProfile(t, d.store, "Work")
	personal := createTestProfile(t, d.store, "Personal")
	closeReopenSession(t, d, reopenSession{
		ID: "orphan", Directory: t.TempDir(), Agent: "codex", Resume: "conv-orphan", ProfileID: work.ID,
	})
	deleteTestProfile(t, d.store, work.ID, defaultProfileID(t, d.store))
	before := spawnCount(backend)

	if _, err := d.reopenSession("orphan", "", "", profileDestination{}); err == nil || !strings.Contains(err.Error(), work.ID) {
		t.Fatalf("reopen without a destination = %v, want a refusal naming the deleted profile %s", err, work.ID)
	}
	if _, err := d.reopenSession("orphan", "", "", profileDestination{requested: work.ID}); err == nil {
		t.Fatal("reopening into the deleted profile itself was accepted")
	}
	if spawnCount(backend) != before || !d.store.SessionClosed("orphan") {
		t.Fatal("a refused reopen spawned or lifted the close")
	}

	outcome, err := d.reopenSession("orphan", "", "", profileDestination{requested: personal.ID})
	if err != nil {
		t.Fatalf("reopen into Personal: %v", err)
	}
	if outcome.ProfileID != personal.ID || d.store.Get("orphan").ProfileID != personal.ID {
		t.Errorf("reopened into %q (row %q), want %s", outcome.ProfileID, d.store.Get("orphan").ProfileID, personal.ID)
	}
}

func TestReopeningIntoAnotherLiveProfileIsAMoveAndRefused(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	reopenDaemonWithBackend(t, d)
	writeCodexRolloutFixture(t, "conv-stay")
	personal := createTestProfile(t, d.store, "Personal")
	closeReopenSession(t, d, reopenSession{
		ID: "stay", Directory: t.TempDir(), Agent: "codex", Resume: "conv-stay",
	})

	if _, err := d.reopenSession("stay", "", "", profileDestination{requested: personal.ID}); err == nil {
		t.Fatal("reopen moved a session out of its live profile")
	}
}

func TestAFailedReopenIntoANewProfileKeepsTheRecordedOne(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	backend := reopenDaemonWithBackend(t, d)
	backend.spawnErr = errSpawnRefusedInThisTest
	writeCodexRolloutFixture(t, "conv-failed-move")
	work := createTestProfile(t, d.store, "Work")
	closeReopenSession(t, d, reopenSession{
		ID: "failed-move", Directory: t.TempDir(), Agent: "codex", Resume: "conv-failed-move", ProfileID: work.ID,
	})
	deleteTestProfile(t, d.store, work.ID, defaultProfileID(t, d.store))

	if _, err := d.reopenSession("failed-move", "", "", profileDestination{requested: defaultProfileID(t, d.store)}); err == nil {
		t.Fatal("the reopen reported success although the spawn failed")
	}
	if profileID, err := d.store.SessionProfileID("failed-move"); err != nil || profileID != work.ID {
		t.Errorf("closed row profile = %q err=%v, want the recorded %s kept as history", profileID, err, work.ID)
	}
}
