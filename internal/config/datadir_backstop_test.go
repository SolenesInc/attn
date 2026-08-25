package config

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The backstop cannot be asserted in-process: this package's TestMain always sets
// ATTN_DATA_DIR, so this re-execs the test binary with it unset in the CHILD's env.
func TestDataDir_PanicsWithoutATTNDataDirUnderTest(t *testing.T) {
	if os.Getenv("ATTN_TEST_DATADIR_BACKSTOP_HELPER") == "1" {
		_ = DataDir()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestDataDir_PanicsWithoutATTNDataDirUnderTest$")
	cmd.Env = childEnvWithout(os.Environ(), "ATTN_DATA_DIR")
	cmd.Env = append(cmd.Env, "ATTN_TEST_DATADIR_BACKSTOP_HELPER=1")
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("subprocess DataDir() call without ATTN_DATA_DIR did not fail; want a panic. output:\n%s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.Success() {
		t.Fatalf("subprocess exited with unexpected error %v (want a crash from panic); output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "ATTN_DATA_DIR is not set under go test") {
		t.Fatalf("subprocess output missing backstop panic message; output:\n%s", out)
	}
}

func childEnvWithout(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
