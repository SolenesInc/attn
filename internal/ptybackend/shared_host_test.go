package ptybackend

import (
	"testing"

	"github.com/victorarias/attn/internal/ptyhost"
)

func TestCurrentSharedHostEntryRequiresMatchingIdentity(t *testing.T) {
	backend := &WorkerBackend{cfg: WorkerBackendConfig{DaemonInstanceID: "daemon"}}
	entry := ptyhost.HostRegistry{
		DaemonInstanceID: "daemon",
		Generation:       "generation",
		SocketPath:       "/tmp/host.sock",
		SnapshotFormat:   "unknown",
	}
	if err := backend.validateCurrentSharedHostEntry(entry, "generation", "/tmp/host.sock"); err != nil {
		t.Fatalf("development build rejected: %v", err)
	}
	entry.Generation = "stale"
	if err := backend.validateCurrentSharedHostEntry(entry, "generation", "/tmp/host.sock"); err == nil {
		t.Fatal("stale host identity was accepted")
	}
}

func TestSharedHostSnapshotFormatGuard(t *testing.T) {
	if err := validateSharedHostSnapshotFormat("current", "current"); err != nil {
		t.Fatalf("matching host rejected: %v", err)
	}
	if err := validateSharedHostSnapshotFormat("stale", "current"); err == nil {
		t.Fatal("stale host snapshot format was accepted for a release build")
	}
	if err := validateSharedHostSnapshotFormat("current", "unknown"); err != nil {
		t.Fatalf("development build cannot use portable replay: %v", err)
	}
}
