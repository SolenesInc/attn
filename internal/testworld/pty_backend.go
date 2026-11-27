package testworld

import (
	"os"
	"testing"
)

func SharedHostBinary(t testing.TB) string {
	t.Helper()
	host := os.Getenv("ATTN_TEST_PTY_HOST")
	if host == "" {
		t.Fatal("ATTN_TEST_PTY_HOST is unset: run this test through scripts/test-go.sh, which provides the PTY host")
	}
	return host
}

func (s *Stack) UseSharedHostBinary(host string) {
	s.Vars = append(s.Vars, "ATTN_PTY_HOST_BINARY="+host)
}
