package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestAStrandedPluginThatIgnoresSIGTERMIsKilledWithItsChildrenWhenAttnStartsAgain(t *testing.T) {
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
	script := fmt.Sprintf("#!/bin/sh\nif [ ! -e %[1]q.started ]; then : > %[1]q.started; trap '' TERM; sleep 600 3>%[1]q & fi\nexec sleep 600\n", fifo)
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
	awaitPipeReleased(t, held, "the child of the SIGTERM-deaf plugin the killed daemon left running")
}
