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
	s.StartWith("ATTN_WORKER_ORPHAN_TTL=1s")
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

	select {
	case err := <-gone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * fakeagent.HangGuard):
		t.Fatalf("the shell still runs %s after its daemon stopped, want its worker gone after the 1s orphan TTL", 2*fakeagent.HangGuard)
	}
}
