package main_test

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAWorkerWhoseDaemonNeverReturnsEndsItsProgramAfterTheOrphanTTL(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	shell := s.Spawn(app, fakeagent.Harness(protocol.SessionAgentShell), s.Path("shop"))
	held := filepath.Join(s.Dir, "held")
	if err := syscall.Mkfifo(held, 0o600); err != nil {
		t.Fatal(err)
	}
	gone := make(chan error, 1)
	go func() {
		f, err := os.Open(held)
		if err == nil {
			_, err = io.Copy(io.Discard, f)
			_ = f.Close()
		}
		gone <- err
	}()
	app.TypeLine(shell, "exec 3>"+held+"; echo held-$((6*7))")
	app.AwaitScreen(shell, "held-42")
	s.Stop()
	s.AwaitTimer(orphanedWorkerTTL)
	select {
	case <-gone:
		t.Fatalf("the shell ended before its worker's %s orphan TTL", orphanedWorkerTTL)
	default:
	}
	s.Advance(orphanedWorkerTTL)

	select {
	case err := <-gone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * fakeagent.HangGuard):
		t.Fatalf("the shell still runs %s after its worker's %s orphan TTL passed", 2*fakeagent.HangGuard, orphanedWorkerTTL)
	}
}

const orphanedWorkerTTL = 12 * time.Hour
