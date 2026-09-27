package daemonctl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func isAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func TestStop_NonContentionFlockErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := exec.Command("sleep", "30")
	if err := helper.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	})
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	originalFlockFn := flockFn
	flockFn = func(fd int, how int) error {
		return syscall.ENOLCK
	}
	t.Cleanup(func() { flockFn = originalFlockFn })

	result, err := Stop(pidPath)
	if err == nil {
		t.Fatalf("Stop() = %+v, err = nil, want an indeterminate-state error", result)
	}
	if !strings.Contains(err.Error(), "cannot determine daemon state") {
		t.Fatalf("Stop() error = %v, want it to mention the indeterminate-state message", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
	if !isAlive(helper.Process.Pid) {
		t.Fatal("helper process is gone: Stop() signaled a pid on an inconclusive flock result")
	}
}
