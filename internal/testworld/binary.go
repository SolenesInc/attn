package testworld

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var attnBinary = sync.OnceValues(buildAttn)

func AttnBinary(t testing.TB) string {
	t.Helper()
	binary, err := attnBinary()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func buildAttn() (string, error) {
	if binary := os.Getenv("ATTN_E2E_BIN"); binary != "" {
		info, err := os.Stat(binary)
		if err != nil {
			return "", fmt.Errorf("ATTN_E2E_BIN: %w", err)
		}
		if info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("ATTN_E2E_BIN is not executable: %s", binary)
		}
		return binary, nil
	}
	if processDir == "" {
		return "", fmt.Errorf("testworld.AttnBinary builds into the process directory that testworld.Main creates; call it from TestMain")
	}
	binary := filepath.Join(processDir, "bin", "attn")
	build := exec.Command("go", "build", "-o", binary, "github.com/victorarias/attn/cmd/attn")
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build attn: %w\n%s", err, output)
	}
	return binary, nil
}
