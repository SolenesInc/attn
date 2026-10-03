package main_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func openHeldPipe(t *testing.T, fifo string) *os.File {
	t.Helper()
	opened := make(chan *os.File, 1)
	go func() {
		f, err := os.Open(fifo)
		if err != nil {
			t.Error(err)
		}
		opened <- f
	}()
	select {
	case f := <-opened:
		if f == nil {
			t.FailNow()
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("nothing opened %s within %s", fifo, fakeagent.HangGuard)
		return nil
	}
}

func awaitPipeReleased(t *testing.T, held *os.File, what string) {
	t.Helper()
	released := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(held)
		released <- err
	}()
	select {
	case err := <-released:
		if err != nil {
			t.Errorf("reading the pipe %s held: %v", what, err)
		}
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("%s still holds its pipe after %s", what, fakeagent.HangGuard)
	}
}

func TestAPluginStrandedByADaemonThatDiedIsKilledWhenAttnStartsAgain(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	checkout, fifo := s.Path("stubborn"), s.Path("stubborn.fifo")
	writePluginSource(t, checkout, "stubborn")
	if err := os.MkdirAll(filepath.Dir(fifo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nif [ ! -e %[1]q.started ]; then : > %[1]q.started; exec 3>%[1]q; fi\nexec sleep 600\n", fifo)
	if err := os.WriteFile(filepath.Join(checkout, "bin", "run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	s.Start()
	if linked := s.Attn("plugin", "link", "--path", checkout); linked.Code != 0 {
		t.Fatalf("plugin link exited %d: %s", linked.Code, linked.Stderr)
	}
	held := openHeldPipe(t, fifo)
	s.Kill()
	s.Start()
	awaitPipeReleased(t, held, "the plugin the killed daemon left running")
}
