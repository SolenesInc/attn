package main_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestDaemonEnsureReportsTheIsolationRefusalOfADaemonOutsideItsDataDir(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	got := s.Run(testworld.Invocation{
		Args: []string{"daemon", "ensure"},
		Env:  []string{"ATTN_SOCKET_PATH=" + filepath.Join(s.Dir, "foreign", "attn.sock")},
	})
	if got.Code != 1 || !strings.Contains(got.Stderr, "daemon ensure error:") || !strings.Contains(got.Stderr, "refusing to start daemon") {
		t.Fatalf("attn daemon ensure exited %d with stderr:\n%s\nwant 1 and the daemon's isolation refusal", got.Code, got.Stderr)
	}
}
