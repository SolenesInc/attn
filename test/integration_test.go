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
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "attn")

	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/attn")
	if err := cmd.Run(); err != nil {
		t.Fatalf("build failed: %v", err)
	}

	config.ScopeTestEnvironment(tmpDir)
	daemon := exec.Command(binPath, "daemon")
	if err := daemon.Start(); err != nil {
		t.Fatalf("daemon start failed: %v", err)
	}
	defer daemon.Process.Kill()

	time.Sleep(100 * time.Millisecond)

	status := exec.Command(binPath, "status")
	output, _ := status.Output()
	t.Logf("status output: %q", output)

	list := exec.Command(binPath, "list")
	output, err := list.Output()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	t.Logf("list output: %s", output)
}
