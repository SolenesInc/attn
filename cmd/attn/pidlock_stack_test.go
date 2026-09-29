package main_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestADaemonThatFailsToStartLeavesThePIDLockToAProcessAlreadyHoldingTheFile(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	pidPath := filepath.Join(filepath.Dir(s.Socket), "attn.pid")
	holder, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	held, err := holder.Stat()
	if err != nil {
		t.Fatal(err)
	}
	// Without fd inheritance, the daemon must refuse the socket held by the stack.
	if got := s.Run(testworld.Invocation{Args: []string{"daemon"}, Env: []string{"ATTN_HARNESS_WS_LISTENER_FD="}}); got.Code != 1 {
		t.Fatalf("attn daemon with its port taken exited %d, want 1\nstderr:\n%s", got.Code, got.Stderr)
	}

	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("locking the pid file after the daemon gave up: %v", err)
	}
	current, err := os.Stat(pidPath)
	if err != nil {
		t.Fatalf("the pid file is gone after the daemon gave up: %v", err)
	}
	if !os.SameFile(held, current) {
		t.Fatal("the daemon replaced the pid file, so the lock just taken guards an orphan while the next daemon locks a new file")
	}
}
