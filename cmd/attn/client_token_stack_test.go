package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheDaemonMintsAnOwnerOnlyClientTokenOncePerDataDir(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	path := filepath.Join(s.Dir, config.ClientTokenFile)
	if unminted := s.Attn("client-token"); unminted.Code == 0 || unminted.Stdout != "" || !strings.Contains(unminted.Stderr, path) {
		t.Errorf("attn client-token before any start exited %d printing %q with stderr %q, want a refusal naming %s", unminted.Code, unminted.Stdout, unminted.Stderr, path)
	}

	token := func(s *testworld.Stack) string {
		t.Helper()
		got := s.Attn("client-token")
		if got.Code != 0 {
			t.Fatalf("attn client-token exited %d: %s", got.Code, got.Stderr)
		}
		return strings.TrimSpace(got.Stdout)
	}
	s.Start()
	minted := token(s)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(minted) {
		t.Fatalf("the minted client token is %q, want 64 hex characters", minted)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("the client token file has mode %#o, want 0600", mode)
	}
	s.Stop()
	s.Start()
	if again := token(s); again != minted {
		t.Errorf("a restart changed the client token from %s to %s", minted, again)
	}

	neighbour := testworld.NewStack(t)
	neighbour.Start()
	if theirs := token(neighbour); theirs == minted {
		t.Errorf("two data dirs minted the same client token %s", minted)
	}

	handed := s.Run(testworld.Invocation{Args: []string{"client-token"}, Env: []string{"ATTN_CLIENT_TOKEN=handed-over"}})
	if strings.TrimSpace(handed.Stdout) != "handed-over" {
		t.Errorf("attn client-token with ATTN_CLIENT_TOKEN set printed %q, want the environment's token", handed.Stdout)
	}
}
