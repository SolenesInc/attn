package ptyhosttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	builds  sync.Map
	buildMu sync.Mutex
)

func BuildWithSnapshotFormat(t testing.TB, format string) string {
	t.Helper()
	build, _ := builds.LoadOrStore(format, sync.OnceValues(func() (string, error) {
		return build(format)
	}))
	binary, err := build.(func() (string, error))()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func build(format string) (string, error) {
	module, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("locate the attn module: %w", err)
	}
	root := strings.TrimSpace(string(module))
	targetDir := filepath.Join(root, "pty-host", "target", "test-formats")
	binary := filepath.Join(targetDir, "formats", format, "attn-pty-host")

	buildMu.Lock()
	defer buildMu.Unlock()
	cmd := exec.Command("cargo", "build", "--quiet", "--locked",
		"--manifest-path", filepath.Join(root, "pty-host", "Cargo.toml"),
		"--target-dir", targetDir)
	cmd.Env = append(os.Environ(), "ATTN_PTY_HOST_SNAPSHOT_FORMAT="+format)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build attn-pty-host with snapshot format %s: %w\n%s", format, err, output)
	}
	built, err := os.ReadFile(filepath.Join(targetDir, "debug", "attn-pty-host"))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		return "", err
	}
	staged := binary + ".tmp"
	if err := os.WriteFile(staged, built, 0o755); err != nil {
		return "", err
	}
	return binary, os.Rename(staged, binary)
}
