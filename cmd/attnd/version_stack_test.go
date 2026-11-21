package main_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheVersionFlagsPrintWhatBootstrapAndTheHarnessRead(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.RunFrom(testworld.AttnBinaryAt(t, "dev"))

	var build struct {
		Version           string `json:"version"`
		Channel           string `json:"channel"`
		BuildTime         string `json:"buildTime"`
		SourceFingerprint string `json:"sourceFingerprint"`
		GitCommit         string `json:"gitCommit"`
		Dirty             bool   `json:"dirty"`
	}
	built := s.Attn("--build-info-json")
	built.JSON(t, &build)
	for key, value := range map[string]string{"version": build.Version, "channel": build.Channel, "buildTime": build.BuildTime, "sourceFingerprint": build.SourceFingerprint} {
		if value == "" {
			t.Errorf("--build-info-json has no %s: %s", key, built.Stdout)
		}
	}
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(string(head)); build.GitCommit != want {
		t.Errorf("--build-info-json reports commit %q, want the checkout's %q", build.GitCommit, want)
	}
	if got := s.Attn("version", "--json"); got.Stdout != built.Stdout {
		t.Errorf("attn version --json printed %q, want what --build-info-json prints %q", got.Stdout, built.Stdout)
	}

	dirty := ""
	if build.Dirty {
		dirty = ", dirty"
	}
	line := fmt.Sprintf("%s (%s, %s%s)", build.Version, build.Channel, build.GitCommit[:8], dirty)
	if got := s.Attn("version"); got.Code != 0 || strings.TrimSpace(got.Stdout) != line {
		t.Errorf("attn version exited %d printing %q, want %q", got.Code, got.Stdout, line)
	}
	if got := s.Attn("--version"); got.Code != 0 || strings.TrimSpace(got.Stdout) != build.Version {
		t.Errorf("attn --version exited %d printing %q, want the bare version %q that release preflight compares", got.Code, got.Stdout, build.Version)
	}
	if got := s.Attn("--protocol-version"); got.Code != 0 || strings.TrimSpace(got.Stdout) != protocol.ProtocolVersion {
		t.Errorf("attn --protocol-version exited %d printing %q, want %q", got.Code, got.Stdout, protocol.ProtocolVersion)
	}
}

func TestTheCLIWarnsWhenItsReleaseDiffersFromTheRunningDaemons(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.RunFrom(testworld.AttnBinaryAt(t, "0.10.2"))
	s.Start()

	for _, tc := range []struct {
		cli    string
		binary string
		warn   bool
	}{
		{cli: "0.5.0", binary: testworld.AttnBinaryAt(t, "0.5.0"), warn: true},
		{cli: "0.10.2", binary: testworld.AttnBinaryAt(t, "0.10.2")},
		{cli: "dev", binary: testworld.AttnBinary(t)},
	} {
		got := s.Run(testworld.Invocation{Args: []string{"update", "status"}, Binary: tc.binary})
		warned := strings.Contains(got.Stderr, "this CLI is "+tc.cli+" but the running attn app is 0.10.2") && strings.Contains(got.Stderr, "`which -a attn`")
		if warned != tc.warn || (!tc.warn && strings.Contains(got.Stderr, "warning")) {
			t.Errorf("attn %s update status against a 0.10.2 daemon printed %q, want a warning: %t", tc.cli, got.Stderr, tc.warn)
		}
	}
}

func TestTheCLIWarnsWhenItsProtocolDiffersFromTheRunningDaemons(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	var reported atomic.Value
	s.ServeWS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"protocol":%q}`, reported.Load())
	}))

	for _, tc := range []struct {
		daemon string
		warn   bool
	}{
		{daemon: "not-this-protocol", warn: true},
		{daemon: protocol.ProtocolVersion},
	} {
		reported.Store(tc.daemon)
		got := s.Run(testworld.Invocation{Args: []string{"conversation", "list"}})
		warned := strings.Contains(got.Stderr, "the running attn app speaks "+tc.daemon) && strings.Contains(got.Stderr, "`which -a attn`")
		if warned != tc.warn {
			t.Errorf("attn conversation list against protocol %s printed %q, want a warning: %t", tc.daemon, got.Stderr, tc.warn)
		}
	}
}

func TestACatalogCommandRefusedByADaemonOnAnotherProtocolNamesTheStaleProtocol(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	var probes atomic.Int32
	s.ServeWS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probes.Add(1)
		_, _ = fmt.Fprint(w, `{"protocol":"an-older-protocol"}`)
	}))
	olderDaemon := serveOlderDaemon(t, s.Socket)

	for _, args := range [][]string{
		{"review", "check", "review.md"},
		{"open", "s-7k3f9m"},
		{"nav", "status"},
		{"seed", "show", "s-7k3f9m"},
		{"session", "transcript", "a-session"},
		{"session", "instructions", "a-session", "--question", "what changed?"},
	} {
		olderDaemon.Store(`{"ok":false,"command_refusal":{"code":"usage","message":"\"` + args[0] + `\" is not a command"}}`)
		got := s.Run(testworld.Invocation{Args: args})
		warning := strings.Index(got.Stderr, "the running attn app speaks an-older-protocol")
		refusal := strings.Index(got.Stderr, "is not a command")
		if got.Code == 0 || warning < 0 || !strings.Contains(got.Stderr, "`which -a attn`") || refusal < warning {
			t.Errorf("attn %s refused by a daemon on another protocol exited %d printing %q, want the stale-protocol warning and its hint, then the refusal", strings.Join(args, " "), got.Code, got.Stderr)
		}
	}

	before := probes.Load()
	olderDaemon.Store(`{"ok":true,"command_result":{"opened":"s-7k3f9m"}}`)
	if got := s.Attn("open", "s-7k3f9m"); got.Code != 0 || strings.Contains(got.Stderr, "warning") {
		t.Errorf("attn open answered by the daemon exited %d printing %q, want it to succeed without a warning", got.Code, got.Stderr)
	}
	if after := probes.Load(); after != before {
		t.Errorf("a successful attn open probed the daemon's health %d times, want none", after-before)
	}
}

func serveOlderDaemon(t *testing.T, socket string) *atomic.Value {
	t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var reply atomic.Value
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var request struct {
				Cmd string `json:"cmd"`
			}
			if json.NewDecoder(bufio.NewReader(conn)).Decode(&request) == nil {
				if request.Cmd == "run_command" {
					_, _ = fmt.Fprintln(conn, reply.Load())
				} else {
					_, _ = fmt.Fprintf(conn, `{"ok":false,"error":"the older daemon was asked %s"}`+"\n", request.Cmd)
				}
			}
			_ = conn.Close()
		}
	}()
	return &reply
}
