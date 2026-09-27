package testworld

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var appHost = sync.OnceValues(buildAppHost)

func UseRealAppRuntime(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is not on PATH; the real app runtime host is a bun --compile binary")
	}
	host, err := appHost()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_APP_RUNTIME_HOST", host)
}

func buildAppHost() (string, error) {
	if processDir == "" {
		return "", fmt.Errorf("testworld.UseRealAppRuntime builds into the process directory that testworld.Main creates; call it from TestMain")
	}
	_, here, _, _ := runtime.Caller(0)
	source := filepath.Join(filepath.Dir(here), "..", "..", "apphost")
	binary := filepath.Join(processDir, "bin", "attn-app-runtime")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return "", err
	}
	build := exec.Command("bun", "build", filepath.Join(source, "src", "index.ts"), "--compile", "--outfile", binary)
	build.Dir = source
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build the app runtime host: %w\n%s", err, output)
	}
	return binary, nil
}
