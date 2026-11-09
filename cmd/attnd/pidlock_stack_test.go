package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
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
	if got := s.Run(testworld.Invocation{Args: []string{"daemon"}, Env: []string{"ATTN_HARNESS_WS_LISTENER_FD="}}); got.Code != 1 || !strings.Contains(got.Stderr, s.WSAddr) {
		t.Fatalf("attn daemon with its port taken exited %d, want 1 naming %s\nstderr:\n%s", got.Code, s.WSAddr, got.Stderr)
	}
	if _, err := os.Stat(s.Socket); !os.IsNotExist(err) {
		t.Fatalf("the refused daemon left its socket behind (stat: %v), a false ready signal", err)
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

func TestADaemonWaitsOutTheLockAGoneDaemonLeftWithItsChild(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	s.Stop()
	pidPath := filepath.Join(filepath.Dir(s.Socket), "attn.pid")
	holdLock := func() *os.File {
		t.Helper()
		child, err := os.OpenFile(pidPath, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = child.Close() })
		locked := make(chan error, 1)
		go func() { locked <- syscall.Flock(int(child.Fd()), syscall.LOCK_EX) }()
		select {
		case err := <-locked:
			if err != nil {
				t.Fatalf("taking the gone daemon's lock: %v", err)
			}
		case <-time.After(fakeagent.HangGuard):
			t.Fatalf("the gone daemon's lock on %s was still held after %s", pidPath, fakeagent.HangGuard)
		}
		return child
	}

	child := holdLock()
	started := make(chan struct{})
	go func() {
		defer close(started)
		s.Start()
	}()
	s.AwaitTimer(daemon.PIDLockReleaseTimer)
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	<-started
	s.Stop()

	gonePID, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	holdLock()
	refused := make(chan testworld.Result, 1)
	go func() { refused <- s.Run(testworld.Invocation{Args: []string{"daemon"}}) }()
	s.AwaitTimer(daemon.PIDLockReleaseTimer)
	s.Advance(daemon.PIDLockReleaseTripwire)
	got := <-refused
	for _, want := range []string{string(gonePID), pidPath, daemon.PIDLockReleaseTripwire.String()} {
		if got.Code != 1 || !strings.Contains(got.Stderr, want) {
			t.Errorf("attn daemon behind a lock that never came free exited %d, want 1 naming %q\nstderr:\n%s", got.Code, want, got.Stderr)
		}
	}
}
