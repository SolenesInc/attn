package ptybackend

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/pty"
)

func TestTerminalRemovalCanRaceItsExitCleanup(t *testing.T) {
	binary := buildAttnBinary(t)
	for _, kind := range []string{"worker", "shared host"} {
		for _, signalFirst := range []bool{false, true} {
			name := kind + "/exit-then-remove"
			if signalFirst {
				name = kind + "/signal-then-remove"
			}
			t.Run(name, func(t *testing.T) {
				root := newWorkerBackendTestRoot(t)
				cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-remove", BinaryPath: binary}
				create := NewWorker
				if kind == "shared host" {
					if os.Getenv("ATTN_TEST_PTY_HOST") == "" {
						t.Skip("set ATTN_TEST_PTY_HOST to verify shared-host removal")
					}
					cfg.BinaryPath = os.Getenv("ATTN_TEST_PTY_HOST")
					create = NewSharedHost
					stopHostsAtCleanup(t, root)
				}
				backend, err := create(cfg)
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
}

func TestWorkerRemovalWaitsForResistantChild(t *testing.T) {
	binary := buildAttnBinary(t)
	root, err := os.MkdirTemp("/tmp", "pty-remove-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewWorker(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-remove", BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Shutdown(context.Background())
	readyPath := filepath.Join(root, "ready")
	if err := syscall.Mkfifo(readyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		f, err := os.Open(readyPath)
		if err != nil {
			ready <- err
			return
		}
		defer f.Close()
		line, err := bufio.NewReader(f).ReadString('\n')
		if err == nil && line != "ready\n" {
			err = fmt.Errorf("unexpected readiness %q", line)
		}
		ready <- err
	}()
	const id = "resistant-child"
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: id, CWD: root, Agent: "cleanup-probe", Cols: 80, Rows: 24,
		AgentArgv: []string{"/bin/sh", "-c", `trap '' TERM HUP; exec 3>"$1"; printf 'ready\n' >&3; while :; do read hold || true; done`, "cleanup-probe", readyPath},
	}); err != nil {
		t.Fatal(err)
	}
	workerPID := backend.WorkerPIDs(context.Background())[id]
	info, err := backend.SessionInfo(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if procreap.ProcessAlive(info.PID) {
			_ = syscall.Kill(-info.PID, syscall.SIGKILL)
		}
		if procreap.ProcessAlive(workerPID) {
			_ = syscall.Kill(workerPID, syscall.SIGKILL)
		}
	}()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	if err := backend.Remove(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if procreap.ProcessAlive(info.PID) {
		t.Fatalf("child %d survived completed removal", info.PID)
	}
}
