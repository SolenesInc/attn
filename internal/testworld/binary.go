package testworld

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	attnBinary = sync.OnceValues(func() (string, error) {
		return provideBinary("ATTN_E2E_BIN", "attn", "github.com/victorarias/attn/cmd/attn")
	})
	fakeAgentBinary = sync.OnceValues(func() (string, error) {
		return provideBinary("ATTN_FAKEAGENT_BIN", "fakeagent", "github.com/victorarias/attn/scripts/fakeagent")
	})
)

func AttnBinary(t testing.TB) string {
	t.Helper()
	binary, err := attnBinary()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func FakeAgentBinary(t testing.TB) string {
	t.Helper()
	binary, err := fakeAgentBinary()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func provideBinary(envVar, name, pkg string) (string, error) {
	if binary := os.Getenv(envVar); binary != "" {
		info, err := os.Stat(binary)
		if err != nil {
			return "", fmt.Errorf("%s: %w", envVar, err)
		}
		if info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("%s is not executable: %s", envVar, binary)
		}
		return binary, nil
	}
	if processDir == "" {
		return "", fmt.Errorf("testworld builds %s into the process directory that testworld.Main creates; call it from TestMain", name)
	}
	binary := filepath.Join(processDir, "bin", name)
	build := exec.Command("go", "build", "-o", binary, pkg)
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s: %w\n%s", name, err, output)
	}
	return binary, nil
}
