package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
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

type failingSpawnBackend struct {
	err error
}

func (b *failingSpawnBackend) Spawn(context.Context, ptybackend.SpawnOptions) error {
	return b.err
}
func (b *failingSpawnBackend) Attach(context.Context, harness.TerminalID, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{}, nil, errors.New("attach unsupported")
}
func (b *failingSpawnBackend) Input(context.Context, harness.TerminalID, []byte) error { return nil }
func (b *failingSpawnBackend) Resize(context.Context, harness.TerminalID, uint16, uint16, uint16, uint16) (ptybackend.ResizeResult, error) {
	return ptybackend.ResizeResult{Changed: true}, nil
}
func (b *failingSpawnBackend) SetTheme(context.Context, harness.TerminalID, pty.TerminalTheme) error {
	return nil
}
func (b *failingSpawnBackend) Kill(context.Context, harness.TerminalID, syscall.Signal) error {
	return nil
}
func (b *failingSpawnBackend) Remove(context.Context, harness.TerminalID) error { return nil }
func (b *failingSpawnBackend) TerminalIDs(context.Context) []harness.TerminalID { return nil }
func (b *failingSpawnBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}
func (b *failingSpawnBackend) Shutdown(context.Context) error { return nil }
