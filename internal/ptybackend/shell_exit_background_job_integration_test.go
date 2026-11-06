package ptybackend

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
)

func TestShellExitIsReportedWhileBackgroundJobHoldsTheTerminal(t *testing.T) {
	host := os.Getenv("ATTN_TEST_PTY_HOST")
	if host == "" {
		t.Skip("set ATTN_TEST_PTY_HOST")
	}
	root := newWorkerBackendTestRoot(t)
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-surv", BinaryPath: host})
	if err != nil {
		t.Fatal(err)
	}
	stopHostsAtCleanup(t, root)
	defer backend.Shutdown(context.Background())

	jobHeld := filepath.Join(root, "job-held")
	if err := syscall.Mkfifo(jobHeld, 0o600); err != nil {
		t.Fatal(err)
	}
	jobEnded := make(chan struct{})
	go func() {
		defer close(jobEnded)
		fifo, err := os.Open(jobHeld)
		if err != nil {
			return
		}
		defer fifo.Close()
		_, _ = io.Copy(io.Discard, fifo)
	}()

	const id harness.TerminalID = "survivor"
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: id, CWD: root, Agent: "surv", Cols: 80, Rows: 24,
		AgentArgv: []string{"/bin/sh", "-c", "trap '' HUP; sleep 120 > " + jobHeld + " & exit 0"}}); err != nil {
		t.Fatal(err)
	}
	defer backend.Remove(context.Background(), id)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := backend.WaitBlock(ctx, id, pty.WaitBlockParams{Until: "exit"})
	if err != nil || !res.Exited {
		t.Fatalf("shell exited but wait --until exit did not see it: res=%+v err=%v", res, err)
	}
	if err := backend.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-jobEnded:
	case <-ctx.Done():
		t.Fatal("the background job survived the removal of its terminal")
	}
}
