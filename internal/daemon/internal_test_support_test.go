package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
)

func decidedReopenVerdict(t *testing.T, d *Daemon, sessionID string) *sessionReopenVerdict {
	t.Helper()
	entry := d.store.SessionLedgerEntry(sessionID)
	if entry == nil {
		t.Fatalf("no ledger row for %s", sessionID)
	}
	verdict, err := d.resolveReopen(context.Background(), *entry, d.scheduledReopenGit())
	if err != nil {
		t.Fatalf("resolve the reopen verdict of %s: %v", sessionID, err)
	}
	return &verdict
}

func drainClientPayloads(t *testing.T, client *wsClient) [][]byte {
	t.Helper()
	var payloads [][]byte
	for {
		select {
		case msg, open := <-client.send:
			if !open {
				return payloads
			}
			payloads = append(payloads, msg.payload)
		default:
			return payloads
		}
	}
}

func homeDaemon(t *testing.T, d *Daemon) *Daemon {
	t.Helper()
	if d.dataRoot == "" {
		d.dataRoot = t.TempDir()
	}
	id, err := enrollment.EnsureDaemonID(d.dataRoot)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	d.daemonInstanceID = id
	if err := d.ensureEnrollment(); err != nil {
		t.Fatalf("ensureEnrollment: %v", err)
	}
	return d
}

func spawnCount(backend *fakeSpawnBackend) int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.spawnOpts)
}

func writeCodexRolloutFixture(t *testing.T, resumeID string) {
	t.Helper()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	sessionsDir := filepath.Join(codexHome, "sessions", "2026", "07", "20")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir Codex sessions dir: %v", err)
	}
	rollout := []byte(`{"type":"session_meta","payload":{"id":"` + resumeID + `","cwd":"/tmp"}}` + "\n")
	if err := os.WriteFile(filepath.Join(sessionsDir, "rollout-"+resumeID+".jsonl"), rollout, 0o644); err != nil {
		t.Fatalf("write Codex rollout fixture: %v", err)
	}
}
