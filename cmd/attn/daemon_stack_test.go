package main_test

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/testworld"
)

func TestADaemonWhoseSocketLeavesItsDataDirNeedsItsOwnDatabase(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	foreign := filepath.Join(s.Dir, "foreign")
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		env     []string
		refused bool
	}{
		{name: "the data dir's database", env: []string{"ATTN_SOCKET_PATH=" + filepath.Join(foreign, "attn.sock")}, refused: true},
		{name: "a relative path naming the data dir's database", env: []string{"ATTN_SOCKET_PATH=" + filepath.Join(foreign, "attn.sock"), "ATTN_DB_PATH=attn.db"}, refused: true},
		{name: "a database of its own", env: []string{"ATTN_SOCKET_PATH=" + filepath.Join(foreign, "attn.sock"), "ATTN_DB_PATH=" + filepath.Join(foreign, "attn.db")}},
		{name: "a socket inside the data dir", env: []string{"ATTN_SOCKET_PATH=" + filepath.Join(s.Dir, "custom.sock")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Run(testworld.Invocation{Args: []string{"daemon", "ensure"}, Env: tc.env, Dir: s.Dir})
			if got.Code == 0 {
				if stopped := s.Run(testworld.Invocation{Args: []string{"daemon", "stop"}, Env: tc.env, Dir: s.Dir}); stopped.Code != 0 {
					t.Errorf("attn daemon stop exited %d: %s", stopped.Code, stopped.Stderr)
				}
			}
			refused := got.Code == 1 && strings.Contains(got.Stderr, "daemon ensure error:") && strings.Contains(got.Stderr, "refusing to start daemon")
			if refused != tc.refused || (!tc.refused && got.Code != 0) {
				t.Errorf("attn daemon ensure exited %d with stderr:\n%s\nwant the isolation refusal: %t", got.Code, got.Stderr, tc.refused)
			}
		})
	}
}

func TestDaemonUnderANamedInstanceOpensWithABannerNamingItsSocketAndPort(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	_, port, err := net.SplitHostPort(s.WSAddr)
	if err != nil {
		t.Fatal(err)
	}

	got := s.Run(testworld.Invocation{Args: []string{"daemon"}, Env: []string{"ATTN_INSTANCE=banner"}})
	banner, _, _ := strings.Cut(got.Stderr, "\n")
	if want := "[attn instance=banner socket=" + s.Socket + " port=" + port + "]"; banner != want {
		t.Fatalf("attn daemon under instance banner opened stderr with %q, want %q\nstderr:\n%s", banner, want, got.Stderr)
	}
}

func TestTheDaemonKeepsItsLogBoundedAndWritingAcrossAStart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	path := filepath.Join(s.Dir, "daemon.log")
	crept := append([]byte("the oldest line goes\n"), bytes.Repeat([]byte("filler from an earlier run\n"), 21<<20/27)...)
	crept = append(crept, "the newest line stays\n"...)
	if err := os.WriteFile(path, crept, 0o644); err != nil {
		t.Fatal(err)
	}

	s.Start()
	s.Stop()

	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, after, truncated := bytes.Cut(log, []byte("daemon.log truncated"))
	if len(log) > 20<<20 || !truncated || bytes.Contains(log, []byte("the oldest line goes")) ||
		!bytes.Contains(log, []byte("the newest line stays")) || !bytes.Contains(after, []byte("WebSocket client connected")) {
		t.Fatalf("a %d-byte daemon.log became %d bytes (truncation marked: %t), want at most 20 MiB keeping the newest lines and what the daemon wrote since", len(crept), len(log), truncated)
	}
}
