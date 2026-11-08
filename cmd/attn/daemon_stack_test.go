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

func TestASessionMayStartADaemonOnItsHomeOnlyFromThatHomesBinary(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	home := canonicalPath(t, s.Dir)
	binary := canonicalPath(t, testworld.AttnBinary(t))
	elsewhere := "/elsewhere/bin/attn"
	otherHome := canonicalPath(t, t.TempDir())
	partHome := filepath.Join(home, "part-home")
	beside := filepath.Join(home, "beside", "attn.db")
	for _, dir := range []string{partHome, filepath.Dir(beside)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	inPart := func(name string) string { return filepath.Join(partHome, name) }
	session := func(home, binary string, more ...string) []string {
		return append([]string{"ATTN_SESSION_HOME=" + home, "ATTN_SESSION_BINARY=" + binary}, more...)
	}

	for _, tc := range []struct {
		name    string
		env     []string
		refusal []string
	}{
		{name: "outside any session"},
		{name: "the binary its home's daemon runs from", env: session(home, binary)},
		{name: "another binary", env: session(home, elsewhere), refusal: []string{"on " + home + " from " + binary, "runs from " + elsewhere, "instance-env", "make install"}},
		{name: "another binary under the default instance's name", env: session(home, elsewhere, "ATTN_INSTANCE=default"), refusal: []string{"on " + home + " from " + binary}},
		{name: "another binary on a named instance", env: session(home, elsewhere, "ATTN_INSTANCE=guarded")},
		{name: "another binary aimed outside the home", env: session(otherHome, elsewhere)},
		{
			name:    "the home's binary with its database moved out",
			env:     session(partHome, binary, "ATTN_DATA_DIR="+partHome, "ATTN_SOCKET_PATH="+inPart("attn.sock"), "ATTN_DB_PATH="+beside),
			refusal: []string{"ATTN_DB_PATH=" + beside + " (outside)", "ATTN_SOCKET_PATH=" + inPart("attn.sock") + " (inside)", "instance-env"},
		},
		{
			name:    "the home's binary with only the data dir moved out",
			env:     session(partHome, binary, "ATTN_DATA_DIR="+filepath.Dir(beside), "ATTN_SOCKET_PATH="+inPart("attn.sock"), "ATTN_DB_PATH="+inPart("attn.db")),
			refusal: []string{"ATTN_DATA_DIR=" + filepath.Dir(beside) + " (outside)", "ATTN_DB_PATH=" + inPart("attn.db") + " (inside)", "instance-env"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Run(testworld.Invocation{Args: []string{"daemon", "ensure"}, Env: tc.env, Dir: s.Dir})
			if tc.refusal == nil {
				if got.Code != 0 {
					t.Fatalf("attn daemon ensure exited %d, want it to start:\n%s", got.Code, got.Stderr)
				}
				if stopped := s.Run(testworld.Invocation{Args: []string{"daemon", "stop"}, Env: tc.env, Dir: s.Dir}); stopped.Code != 0 {
					t.Errorf("attn daemon stop exited %d: %s", stopped.Code, stopped.Stderr)
				}
				return
			}
			if got.Code != 1 || !strings.Contains(got.Stderr, "refusing to start a daemon") {
				t.Fatalf("attn daemon ensure exited %d, want the session refusal:\n%s", got.Code, got.Stderr)
			}
			for _, want := range tc.refusal {
				if !strings.Contains(got.Stderr, want) {
					t.Errorf("refusal does not name %q:\n%s", want, got.Stderr)
				}
			}
		})
	}
}

func TestARefusedBuildLeavesTheSessionsDaemonRunning(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	pidPath := filepath.Join(s.Dir, "attn.pid")
	before, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"ATTN_SESSION_HOME=" + s.Dir, "ATTN_SESSION_BINARY=" + canonicalPath(t, testworld.AttnBinary(t))}
	other := testworld.AttnBinaryAt(t, "0.0.1-other-build")

	for _, args := range [][]string{{"_bridge"}, {"daemon", "ensure"}, {"daemon"}, {"db", "restore"}} {
		got := s.Run(testworld.Invocation{Args: args, Env: env, Binary: other})
		if got.Code != 1 || !strings.Contains(got.Stderr, "refusing to start a daemon") {
			t.Errorf("attn %v from another build exited %d, want the session refusal:\n%s", args, got.Code, got.Stderr)
		}
	}
	if after, err := os.ReadFile(pidPath); err != nil || string(after) != string(before) {
		t.Fatalf("daemon PID after refusals = %q, %v; want %q", after, err, before)
	}
	if got := s.Attn("daemon", "ensure"); got.Code != 0 || !strings.Contains(got.Stdout, `"already_running"`) {
		t.Fatalf("ensure from the session's own binary after the refusals: %+v; want already_running", got)
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestDaemonUnderANamedInstanceOpensWithABannerNamingItsSocketAndPort(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	_, port, err := net.SplitHostPort(s.WSAddr)
	if err != nil {
		t.Fatal(err)
	}

	env := []string{"ATTN_INSTANCE=banner"}
	t.Cleanup(func() {
		if result := s.Run(testworld.Invocation{Args: []string{"daemon", "stop"}, Env: env}); result.Code != 0 {
			t.Errorf("stop the named daemon: %+v", result)
		}
	})
	got := s.Run(testworld.Invocation{Args: []string{"daemon", "ensure"}, Env: env})
	if got.Code != 0 {
		t.Fatalf("start the named daemon: %+v", got)
	}
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
