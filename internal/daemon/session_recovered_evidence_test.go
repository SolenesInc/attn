package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

func addRecoveredSession(t *testing.T, d *Daemon, id string, state protocol.SessionState, stateSince time.Time) {
	t.Helper()
	stamp := stateSince.UTC().Format(time.RFC3339Nano)
	d.store.Add(&protocol.Session{
		ID:             id,
		Label:          id,
		Agent:          protocol.SessionAgentClaude,
		Directory:      "/tmp/" + id,
		State:          state,
		StateSince:     stamp,
		StateUpdatedAt: stamp,
		LastSeen:       stamp,
	})
}

func runningInfo(signal *pty.Observation) ptybackend.SessionInfo {
	info := ptybackend.SessionInfo{
		Running: true,
		Agent:   string(protocol.SessionAgentClaude),
		CWD:     "/tmp/recovered",
		State:   protocol.StateWorking,
	}
	if signal != nil {
		info.LastSignal = *signal
		info.HasLastSignal = true
	}
	return info
}

func TestReconcileMarksExitedWorkerIdle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addRecoveredSession(t, d, "dead", protocol.SessionStateWorking, time.Now().Add(-time.Hour))
	info := runningInfo(nil)
	info.Running = false
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"dead"},
		info:    map[string]ptybackend.SessionInfo{"dead": info},
	}

	d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})

	if got := d.store.Get("dead").State; got != protocol.SessionStateIdle {
		t.Fatalf("recovered state = %q, want idle", got)
	}
}
