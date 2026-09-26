package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestTheDaemonSocketAcceptsOnceItExistsAndReplacesAStaleOne(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	crashed, err := net.Listen("unix", s.Socket)
	if err != nil {
		t.Fatal(err)
	}
	crashed.(*net.UnixListener).SetUnlinkOnClose(false)
	crashed.Close()
	if _, err := os.Stat(s.Socket); err != nil {
		t.Fatalf("the stale socket a crashed daemon leaves behind is missing: %v", err)
	}

	s.Start()

	if got := s.Attn("agent", "list"); got.Code != 0 || !strings.Contains(got.Stdout, "No sessions on this daemon.") {
		t.Fatalf("attn agent list right after the daemon started exited %d with stdout %q and stderr %q", got.Code, got.Stdout, got.Stderr)
	}
	staged, err := filepath.Glob(filepath.Join(s.Dir, "*.listen"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) > 0 {
		t.Fatalf("the daemon left staging sockets behind: %v", staged)
	}
}
