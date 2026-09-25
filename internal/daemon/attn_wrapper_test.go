package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var testProcessDir string

var buildAttnWrapper = sync.OnceValues(func() (string, error) {
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
	binary := filepath.Join(testProcessDir, "attn")
	build := exec.Command("go", "build", "-o", binary, "github.com/victorarias/attn/cmd/attn")
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build attn: %w\n%s", err, output)
	}
	return binary, nil
})

func AttnWrapper(t testing.TB) string {
	t.Helper()
	binary, err := buildAttnWrapper()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}
