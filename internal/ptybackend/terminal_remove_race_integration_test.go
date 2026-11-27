package ptybackend

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
)

func TestTerminalRemovalCanRaceItsExitCleanup(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to verify shared-host removal")
	}
	for _, signalFirst := range []bool{false, true} {
		name := "exit-then-remove"
		if signalFirst {
			name = "signal-then-remove"
		}
		t.Run(name, func(t *testing.T) {
			root := newWorkerBackendTestRoot(t)
			stopHostsAtCleanup(t, root)
			backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-remove", BinaryPath: binary})
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Shutdown(context.Background())
			watching := make(chan struct{})
			var observed sync.Once
			backend.SetStateHandler(func(harness.TerminalID, pty.Observation) {
				observed.Do(func() { close(watching) })
			})
			removed := make(chan error, 1)
			exiting, cleanup := make(chan struct{}), make(chan struct{})
			var released sync.Once
			release := func() { released.Do(func() { close(cleanup) }) }
			defer release()
			backend.SetExitHandler(func(info ExitInfo) {
				close(exiting)
				<-cleanup
				removed <- backend.Remove(context.Background(), info.ID)
			})
			const id = "concurrent-removal"
			if err := backend.Spawn(context.Background(), SpawnOptions{
				ID: id, CWD: root, Agent: "removal-probe", Cols: 80, Rows: 24,
				AgentArgv: []string{"/bin/cat"},
			}); err != nil {
				t.Fatal(err)
			}
			<-watching
			if signalFirst {
				if err := backend.Kill(context.Background(), id, syscall.SIGTERM); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
					t.Fatal(err)
				}
			} else if err := backend.Input(context.Background(), id, []byte{4}); err != nil {
				t.Fatal(err)
			}
			<-exiting
			release()
			if err := backend.Remove(context.Background(), id); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
				t.Fatal(err)
			}
			if err := <-removed; err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
				t.Fatal(err)
			}
			if ids := backend.TerminalIDs(context.Background()); len(ids) != 0 {
				t.Fatalf("removed terminal remains registered: %v", ids)
			}
		})
	}
}
