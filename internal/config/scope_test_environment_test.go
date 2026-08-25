package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// An inherited ATTN_DB_PATH pointing at the real ~/.attn/attn.db still resolves there
// unless ScopeTestEnvironment clears it, so the hostile values go into a re-exec.
func TestScopeTestEnvironment_SanitizesInheritedOverrides(t *testing.T) {
	const hostileDB = "/tmp/attn-hostile-inherited-test.db"
	const hostileSocket = "/tmp/attn-hostile-inherited-test.sock"
	const hostileConfig = "/tmp/attn-hostile-inherited-config.json"

	if os.Getenv("ATTN_TEST_HOSTILE_OVERRIDE_HELPER") == "1" {
		dataDir := os.Getenv("ATTN_DATA_DIR")
		if dataDir == "" {
			panic("helper: ATTN_DATA_DIR unexpectedly empty inside TestMain-scoped subprocess")
		}
		if got := DBPath(); got == hostileDB || !strings.HasPrefix(got, dataDir) {
			panic("helper: DBPath() escaped ATTN_DATA_DIR scope: got " + got + ", want prefix " + dataDir)
		}
		if got := SocketPath(); got == hostileSocket || !strings.HasPrefix(got, dataDir) {
			panic("helper: SocketPath() escaped ATTN_DATA_DIR scope: got " + got + ", want prefix " + dataDir)
		}
		return
	}

	env := os.Environ()
	for _, key := range []string{"ATTN_DB_PATH", "ATTN_SOCKET_PATH", "ATTN_CONFIG_PATH"} {
		env = childEnvWithout(env, key)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestScopeTestEnvironment_SanitizesInheritedOverrides$")
	cmd.Env = append(env,
		"ATTN_TEST_HOSTILE_OVERRIDE_HELPER=1",
		"ATTN_DB_PATH="+hostileDB,
		"ATTN_SOCKET_PATH="+hostileSocket,
		"ATTN_CONFIG_PATH="+hostileConfig,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess with inherited hostile overrides failed: %v\noutput:\n%s", err, out)
	}
}
