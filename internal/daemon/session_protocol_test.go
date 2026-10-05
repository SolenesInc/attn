package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func TestStartupPrunesRuntimesThatHaveNoSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{sessionIDs: []string{"session-kept", "runtime-orphan"}}
	d.ptyBackend = backend
	d.store.Add(&protocol.Session{ID: "session-kept", Directory: t.TempDir(), State: protocol.SessionStateIdle, Agent: protocol.SessionAgentShell})

	d.pruneRuntimesWithoutSession(context.Background())

	if removed := backend.RemovedIDs(); !slices.Equal(removed, []string{"runtime-orphan"}) {
		t.Fatalf("removed runtimes = %v, want only the one without a session", removed)
	}
}

func newProtocolTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 32),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func expectSpawnResult(t *testing.T, client *wsClient, sessionID string, success bool) protocol.SpawnResultMessage {
	t.Helper()
	deadline := time.After(1 * time.Second)
	for {
		select {
		case outbound := <-client.send:
			var result protocol.SpawnResultMessage
			if err := json.Unmarshal(outbound.payload, &result); err != nil || result.Event != protocol.EventSpawnResult {
				continue
			}
			if result.ID != sessionID {
				continue
			}
			if result.Success != success {
				t.Fatalf("spawn success = %v, want %v; payload=%s", result.Success, success, string(outbound.payload))
			}
			return result
		case <-deadline:
			t.Fatalf("timed out waiting for spawn_result for %s", sessionID)
		}
	}
}
