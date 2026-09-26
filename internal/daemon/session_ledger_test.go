package daemon

import (
	"io"
	"net"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func addLedgerTestSession(t *testing.T, d *Daemon, id, directory string) {
	t.Helper()
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentClaude,
		Directory: directory, WorkspaceID: "ws-" + id,
		State:      protocol.SessionStateWaitingInput,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func drainedConn(t *testing.T) net.Conn {
	t.Helper()
	server, client := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, client) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	return server
}

func TestARuntimeOutlivingItsCloseIsStoppedRatherThanRebuilt(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	addLedgerTestSession(t, d, "stubborn", t.TempDir())
	if _, err := d.store.CloseSession("stubborn", store.SessionClose{By: store.SessionClosedByUser}, time.Now()); err != nil {
		t.Fatalf("close: %v", err)
	}
	d.store.ClearSessionIntentionalClose("stubborn")
	backend := &fakeSpawnBackend{sessionIDs: []string{"stubborn"}}
	d.ptyBackend = backend

	d.reconcileSessionsWithWorkerBackendState(t.Context(), false, false, d.storedSessionIDs(), time.Time{})

	if got := d.store.Get("stubborn"); got != nil {
		t.Errorf("Get = %+v, want the reconcile to leave the session closed", got)
	}
	if killed := backend.killedSessionIDs(); !slices.Contains(killed, "stubborn") {
		t.Errorf("terminated %v, want the closed session's runtime stopped", killed)
	}
}

func (b *fakeSpawnBackend) killedSessionIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.killed)
}
