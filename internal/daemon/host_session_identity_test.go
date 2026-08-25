package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

// Observed missing on a delegated conversation agent: its tools saw an empty
// ATTN_SESSION_ID and resolved `attn` off the login shell's PATH.

func envDumpingHostCommand(t *testing.T) (argv []string, dumpPath string) {
	t.Helper()
	dir := t.TempDir()
	dumpPath = filepath.Join(dir, "env.txt")
	script := filepath.Join(dir, "dump-env-host.sh")
	body := "#!/bin/sh\nenv > " + dumpPath + "\nwhile IFS= read -r line; do :; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write env-dumping host: %v", err)
	}
	return []string{script}, dumpPath
}

func readHostEnv(t *testing.T, dumpPath string) map[string]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(dumpPath)
		if err == nil && len(data) > 0 {
			env := map[string]string{}
			for _, line := range strings.Split(string(data), "\n") {
				name, value, ok := strings.Cut(line, "=")
				if ok {
					env[name] = value
				}
			}
			return env
		}
		if time.Now().After(deadline) {
			t.Fatalf("the host never wrote its environment to %s", dumpPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConversationHostCarriesTheSessionIdentity(t *testing.T) {
	activeAttnDir := t.TempDir()
	activeAttn := filepath.Join(activeAttnDir, "attn")
	if err := os.WriteFile(activeAttn, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write the active attn: %v", err)
	}
	t.Setenv("ATTN_WRAPPER_PATH", activeAttn)

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	t.Setenv("ATTN_PROFILE", "fixture-lab")
	t.Setenv("ATTN_SOCKET_PATH", socketPath)
	t.Setenv("ATTN_WS_PORT", "25432")
	d := NewForTesting(socketPath)
	d.loginShellEnv = []string{
		"ATTN_PROFILE=default",
		"ATTN_DATA_DIR=/tmp/login-data",
		"ATTN_DB_PATH=/tmp/login.db",
		"ATTN_SOCKET_PATH=/tmp/login-shell.sock",
		"ATTN_WS_PORT=19999",
		"ATTN_CONFIG_PATH=/tmp/login-config.json",
		"ATTN_PLUGIN_DIR=/tmp/login-plugins",
		"PATH=" + os.Getenv("PATH"),
	}
	backend := &fakeSpawnBackend{}
	workspaceID, _, cwd := setupDelegationSource(t, d, backend)
	pipe, done := startPluginPipe(t, d, "pi-fixture-plugin", nil)
	defer func() {
		_ = pipe.Close()
		<-done
	}()
	registerTestPluginDriver(t, pipe, "pi-fixture", map[string]bool{
		pluginDriverConversationCapability: true,
		"initial_prompt":                   true,
	})
	argv, dumpPath := envDumpingHostCommand(t)
	serveOneDriverSpawn(t, pipe, argv)

	const sessionID = "conv-identity"
	client := newWorkspaceProtocolTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		WorkspaceID: workspaceID,
		Agent:       "pi-fixture",
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)
	t.Cleanup(func() { _ = d.ensureHostSessions().Kill(sessionID) })

	env := readHostEnv(t, dumpPath)
	for name, want := range map[string]string{
		"ATTN_SESSION_ID":     sessionID,
		"ATTN_AGENT":          "pi-fixture",
		"ATTN_DAEMON_MANAGED": "1",
		"ATTN_INSIDE_APP":     "1",
		"ATTN_PROFILE":        "fixture-lab",
		"ATTN_DATA_DIR":       d.dataRoot,
		"ATTN_DB_PATH":        config.DBPath(),
		"ATTN_SOCKET_PATH":    socketPath,
		"ATTN_WS_PORT":        config.WSPort(),
		"ATTN_CONFIG_PATH":    config.ConfigPath(),
		"ATTN_PLUGIN_DIR":     d.pluginDir,
	} {
		if env[name] != want {
			t.Errorf("host env %s = %q, want %q — the agent's tools cannot report as this session", name, env[name], want)
		}
	}

	// The `attn` those tools find must be the one that spawned them: the login shell's
	// PATH would make a non-production session report into production.
	entries := filepath.SplitList(env["PATH"])
	if len(entries) == 0 || entries[0] != activeAttnDir {
		t.Errorf("host PATH = %q, want the active attn's directory %q first", env["PATH"], activeAttnDir)
	}
}
