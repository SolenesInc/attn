package testworld

import (
	"os"
	"strings"
	"testing"
)

type PTYBackend string

const (
	WorkerPTY         PTYBackend = "worker"
	SharedHostPTY     PTYBackend = "shared host"
	SharedHostOnlyPTY PTYBackend = "shared"
)

var WorkerAndSharedHost = []PTYBackend{WorkerPTY, SharedHostPTY}

func OnPTYBackend(backend PTYBackend) StackOption {
	return func(s *stackSetup) { s.backend = backend }
}

func SharedHostBinary(t testing.TB) string {
	t.Helper()
	host := os.Getenv("ATTN_TEST_PTY_HOST")
	if host == "" {
		t.Fatal("ATTN_TEST_PTY_HOST is unset: run this test through scripts/test-go.sh, which provides the PTY host")
	}
	return host
}

func (s *Stack) UseSharedHostBinary(host string) {
	s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
}

func (s *Stack) RestartOnTheSharedHost() {
	s.T.Helper()
	s.Stop()
	kept := s.Vars[:0]
	for _, v := range s.Vars {
		if !strings.HasPrefix(v, "ATTN_PTY_BACKEND=") && !strings.HasPrefix(v, "ATTN_PTY_HOST_BINARY=") {
			kept = append(kept, v)
		}
	}
	s.Vars = kept
	s.UseSharedHostBinary(SharedHostBinary(s.T))
	s.Start()
}

func (s *Stack) usePTYBackend(backend PTYBackend) {
	s.T.Helper()
	switch backend {
	case SharedHostPTY:
		s.UseSharedHostBinary(SharedHostBinary(s.T))
	case SharedHostOnlyPTY:
		s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=shared", "ATTN_PTY_HOST_BINARY="+SharedHostBinary(s.T))
	default:
		s.Vars = append(s.Vars, "ATTN_PTY_BACKEND="+string(backend))
	}
}
