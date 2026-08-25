//go:build integration

package test

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
)

func TestIntegration_DaemonAndClient(t *testing.T) {
	// Build the binary
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "attn")

	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/attn")
	if err := cmd.Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Scope the daemon subprocess's data dir instead of redirecting HOME. tmpDir
	// directly: ATTN_DATA_DIR/attn.sock must stay under the ~104-char socket limit.
	config.ScopeTestEnvironment(tmpDir)
	daemon := exec.Command(binPath, "daemon")
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer daemon.Process.Kill()

	// Wait for daemon
	time.Sleep(100 * time.Millisecond)

	// Test status command
	status := exec.Command(binPath, "status")
	output, _ := status.Output()
	t.Logf("status output: %q", output)

	// Test list command
	list := exec.Command(binPath, "list")
	output, err := list.Output()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	t.Logf("list output: %s", output)
}
