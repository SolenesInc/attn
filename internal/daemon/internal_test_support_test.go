package daemon

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func addTurnSession(t *testing.T, d *Daemon, id string, agent protocol.SessionAgent) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             id,
		Agent:          agent,
		Label:          id,
		Directory:      "/tmp/" + id,
		ProfileID:      defaultProfileID(t, d.store),
		State:          protocol.StateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
}

func crewSet(t *testing.T, d *Daemon, msg protocol.CrewSetMessage) protocol.Response {
	t.Helper()
	msg.Cmd = protocol.CmdCrewSet
	return gardenCall(t, func(c net.Conn) { d.handleCrewSet(c, &msg) })
}

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

func gitRevParseDaemon(t *testing.T, dir, rev string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse %s in %s failed: %v", rev, dir, err)
	}
	return strings.TrimSpace(string(out))
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

func initDelegationRepo(t *testing.T, root, name string) string {
	t.Helper()
	repo := filepath.Join(root, name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "init")
	return git.CanonicalizePath(repo)
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
