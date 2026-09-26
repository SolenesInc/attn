package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func newReloadClientTestDaemon(t *testing.T, intent *store.LaunchIntent) (*Daemon, *fakeSpawnBackend, *wsClient) {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "session",
		Label:          "session",
		Agent:          protocol.SessionAgentClaude,
		Directory:      cwd,
		WorkspaceID:    "workspace",
		State:          protocol.SessionStateIdle,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if intent != nil {
		d.store.SetLaunchIntent("session", *intent)
	}
	return d, backend, spawnTestClient()
}

func readReloadSessionResult(t *testing.T, client *wsClient) protocol.ReloadSessionResultMessage {
	t.Helper()
	outbound := <-client.send
	var result protocol.ReloadSessionResultMessage
	if err := json.Unmarshal(outbound.payload, &result); err != nil {
		t.Fatalf("decode reload_session_result: %v", err)
	}
	return result
}

func TestReloadSessionRejectsLedgerRowWhileTeardownIsInFlight(t *testing.T) {
	d, backend, client := newReloadClientTestDaemon(t, &store.LaunchIntent{})
	d.teardownMu.Lock()
	if d.tearingDown == nil {
		d.tearingDown = make(map[string]chan struct{})
	}
	d.tearingDown["session"] = make(chan struct{})
	d.teardownMu.Unlock()

	d.handleReloadSession(client, &protocol.ReloadSessionMessage{
		Cmd: protocol.CmdReloadSession, ID: "session", Cols: 80, Rows: 24,
	})

	result := readReloadSessionResult(t, client)
	if result.Success || result.Error == nil || !strings.Contains(*result.Error, "closing") {
		t.Fatalf("reload result = %+v, want closing failure", result)
	}
	if _, spawned := backend.LastSpawn(); spawned {
		t.Fatal("backend Spawn called during teardown")
	}
}
