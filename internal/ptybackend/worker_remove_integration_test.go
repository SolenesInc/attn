package ptybackend

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/procreap"
)

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
		ExternalCommand: []string{"/bin/sh", "-c", `trap '' TERM HUP; exec 3>"$1"; printf 'ready\n' >&3; while :; do read hold || true; done`, "cleanup-probe", readyPath},
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
