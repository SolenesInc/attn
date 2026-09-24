package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
)

func startDelegationRolesDaemon(t *testing.T) {
	t.Helper()
	t.Setenv(toolhome.EnvVar, t.TempDir())
	t.Setenv("ATTN_PTY_BACKEND", "embedded")
	t.Setenv("ATTN_PTY_SKIP_STARTUP_PROBE", "1")
	t.Setenv("ATTN_MOCK_GH_URL", "http://127.0.0.1:1")
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_WS_PORT", strconv.Itoa(port.Addr().(*net.TCPAddr).Port))
	port.Close()
	dir, err := os.MkdirTemp("", "attn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "attn.sock")
	t.Setenv("ATTN_SOCKET_PATH", socket)
	d := daemon.NewForTesting(socket)
	go d.Start()
	t.Cleanup(d.Stop)
	for deadline := time.Now().Add(5 * time.Second); ; {
		if conn, err := net.Dial("unix", socket); err == nil {
			conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDelegationRolesCommandsEditWalkBackAndRestoreTheDaemonsTable(t *testing.T) {
	startDelegationRolesDaemon(t)
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := delegateRoles(&out, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	expect := func(output string, lines ...string) {
		t.Helper()
		for _, line := range lines {
			if !strings.Contains(output, line) {
				t.Fatalf("missing %q in:\n%s", line, output)
			}
		}
	}
	refused := func(want string, args ...string) {
		t.Helper()
		if err := delegateRoles(&bytes.Buffer{}, args); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%v: %v; want an error containing %q", args, err, want)
		}
	}

	expect(run("add", "build", "--name", "Build", "--agent", "claude", "--model", "opus", "--effort", "high", "-m", "the user wants a builder"),
		"revision 1", "added role build (claude opus high)")
	expect(run("add", "build/hard", "--when", "Concurrency", "--agent", "codex", "--model", "gpt-5.6-sol"),
		"build: added alternative hard (codex gpt-5.6-sol)")
	expect(run("set", "build", "--model", "sonnet"), "revision 3", "build: claude opus high → claude sonnet")
	refused("needs a default choice", "rm", "build/default")

	var exported protocol.DelegationPreferences
	if err := json.Unmarshal([]byte(run("show", "--json")), &exported); err != nil || exported.Revision != 3 {
		t.Fatalf("export after a refused edit: revision %d, %v", exported.Revision, err)
	}
	slices.Reverse(exported.Roles[0].Choices)
	raw, _ := json.Marshal(exported)
	edited := filepath.Join(t.TempDir(), "roles.json")
	if err := os.WriteFile(edited, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	expect(run("apply", edited), "revision 4", "build: reordered alternatives")
	refused("the table changed after revision 3", "apply", edited)

	expect(run("rollback"), "revision 5 restores revision 3", "build: reordered alternatives")
	expect(run("rollback"), "revision 6 restores revision 2", "build: claude sonnet → claude opus high")
	expect(run("rollback", "4"), "revision 7 restores revision 4", "build: claude opus high → claude sonnet", "build: reordered alternatives")
	expect(run("show"), "revision 7", "build", "claude sonnet", "/hard", "when: Concurrency")
	expect(run("history", "--limit", "7"), "revision 7 (live)", "restores 4", "from the CLI", `"the user wants a builder"`, "added role build (claude opus high)")
}
