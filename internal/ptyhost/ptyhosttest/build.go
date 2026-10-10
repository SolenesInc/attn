package ptyhosttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

var (
	builds  sync.Map
	buildMu sync.Mutex
)

func BuildWithSnapshotFormat(t testing.TB, format string) string {
	t.Helper()
	return cachedBuild(t, format, format, false)
}

func BuildWithAdoptFault(t testing.TB, format string) string {
	t.Helper()
	return cachedBuild(t, format+"-adopt-fault", format, true)
}

func cachedBuild(t testing.TB, name, format string, adoptFault bool) string {
	t.Helper()
	build, _ := builds.LoadOrStore(name, sync.OnceValues(func() (string, error) {
		return build(name, format, adoptFault)
	}))
	binary, err := build.(func() (string, error))()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func build(name, format string, adoptFault bool) (string, error) {
	module, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("locate the attn module: %w", err)
	}
	root := strings.TrimSpace(string(module))
	targetDir := filepath.Join(root, "pty-host", "target", "test-formats")
	binary := filepath.Join(targetDir, "formats", name, "attn-pty-host")

	buildMu.Lock()
	defer buildMu.Unlock()
	unlock, err := lockTargetDir(targetDir)
	if err != nil {
		return "", err
	}
	defer unlock()
	args := []string{"build", "--quiet", "--locked",
		"--manifest-path", filepath.Join(root, "pty-host", "Cargo.toml"),
		"--target-dir", targetDir}
	if adoptFault {
		args = append(args, "--features", "adopt-fault")
	}
	cmd := exec.Command("cargo", args...)
	cmd.Env = append(os.Environ(), "ATTN_PTY_HOST_SNAPSHOT_FORMAT="+format)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build attn-pty-host %s: %w\n%s", name, err, output)
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

func lockTargetDir(targetDir string) (func(), error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(targetDir, "build.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock %s: %w", lock.Name(), err)
	}
	return func() { _ = lock.Close() }, nil
}
