package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

func spawnTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

type fakeReloadBackend struct {
	mu        sync.Mutex
	liveIDs   []string
	info      ptybackend.SessionInfo
	params    ptybackend.SessionLaunchParams
	paramsErr error
	spawnErr  error
}

func (b *fakeReloadBackend) Spawn(_ context.Context, opts ptybackend.SpawnOptions) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spawnErr != nil {
		return b.spawnErr
	}
	for _, id := range b.liveIDs {
		if id == opts.ID {
			return fmt.Errorf("session %s already exists", opts.ID)
		}
	}
	b.liveIDs = append(b.liveIDs, opts.ID)
	return nil
}

func (b *fakeReloadBackend) Attach(context.Context, string, string, ...ptybackend.AttachOptions) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return ptybackend.AttachInfo{Running: true}, newFakeOutputStream(), nil
}

func (b *fakeReloadBackend) Input(context.Context, string, []byte) error { return nil }

func (b *fakeReloadBackend) Resize(context.Context, string, uint16, uint16, uint16, uint16) (ptybackend.ResizeResult, error) {
	return ptybackend.ResizeResult{Changed: true}, nil
}

func (b *fakeReloadBackend) SetTheme(context.Context, string, pty.TerminalTheme) error {
	return nil
}

func (b *fakeReloadBackend) Kill(_ context.Context, id string, _ syscall.Signal) error {
	b.mu.Lock()
	b.liveIDs = removeReloadID(b.liveIDs, id)
	b.mu.Unlock()
	return nil
}

func (b *fakeReloadBackend) Remove(_ context.Context, id string) error {
	b.mu.Lock()
	b.liveIDs = removeReloadID(b.liveIDs, id)
	b.mu.Unlock()
	return nil
}

func removeReloadID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
}

func (b *fakeReloadBackend) SessionIDs(context.Context) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.liveIDs...)
}

func (b *fakeReloadBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	return ptybackend.RecoveryReport{}, nil
}

func (b *fakeReloadBackend) Shutdown(context.Context) error { return nil }

func (b *fakeReloadBackend) SessionInfo(context.Context, string) (ptybackend.SessionInfo, error) {
	return b.info, nil
}

func (b *fakeReloadBackend) SessionLaunchParams(context.Context, string) (ptybackend.SessionLaunchParams, error) {
	return b.params, b.paramsErr
}

func newReloadTestDaemon(t *testing.T, backend *fakeReloadBackend) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = backend
	return d
}

func addReloadSessionAt(d *Daemon, id string, agent protocol.SessionAgent, state protocol.SessionState, directory string) {
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: agent, Directory: directory,
		WorkspaceID: "ws-" + id, State: state, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}
