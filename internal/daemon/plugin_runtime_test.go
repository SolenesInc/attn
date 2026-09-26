package daemon

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/plugins"
	"github.com/victorarias/attn/internal/procreap"
)

func TestPluginCommandEnv_UsesLoginShellEnvironment(t *testing.T) {
	t.Setenv("PATH", "/daemon/bin")

	d := NewForTesting(filepath.Join(t.TempDir(), "plugin-runtime.sock"))
	d.loginShellEnv = []string{
		"PATH=/user/bin:/usr/bin:/bin",
		"USER_ONLY=present",
	}

	env := d.pluginCommandEnv("ATTN_PLUGIN_NAME=test-plugin")
	if got := envValue(env, "PATH"); got != "/user/bin:/usr/bin:/bin" {
		t.Fatalf("PATH=%q, want login-shell PATH", got)
	}
	if got := envValue(env, "USER_ONLY"); got != "present" {
		t.Fatalf("USER_ONLY=%q, want present", got)
	}
	if got := envValue(env, "ATTN_PLUGIN_NAME"); got != "test-plugin" {
		t.Fatalf("ATTN_PLUGIN_NAME=%q, want test-plugin", got)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func writeTestPluginManifest(t *testing.T, pluginDir, name string) {
	t.Helper()
	root := filepath.Join(pluginDir, name)
	entrypointDir := filepath.Join(root, "src")
	if err := os.MkdirAll(entrypointDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(entrypointDir, "index.ts"), []byte("// fake entrypoint\n"), 0o644); err != nil {
		t.Fatalf("write fake entrypoint: %v", err)
	}
	manifest := []byte(`
name = "` + name + `"
version = "0.1.0"
attn_api_version = 6

[plugin]
entrypoint = "src/index.ts"
`)
	if err := os.WriteFile(filepath.Join(root, pluginManifestName), manifest, 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
}

func dialPluginHelper(socketPath string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, os.ErrDeadlineExceeded
}

func TestReapStrandedPluginRuntimesKillsThemAndRetiresTheirRecords(t *testing.T) {
	dataDir := t.TempDir()
	registryDir := plugins.RuntimeRegistryDir(dataDir)

	script := filepath.Join(dataDir, "stranded.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile true; do sleep 0.05; done\n"), 0o755); err != nil {
		t.Fatalf("write stranded runtime: %v", err)
	}
	cmd := exec.Command(script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stranded runtime: %v", err)
	}
	pid := cmd.Process.Pid
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-exited
	})

	livePath := filepath.Join(registryDir, "attn-pi-live.json")
	if err := procreap.WriteEntry(livePath, procreap.NewEntry("attn-pi", pid, pid, cmd.Args)); err != nil {
		t.Fatalf("write live record: %v", err)
	}
	gonePath := filepath.Join(registryDir, "attn-pi-gone.json")
	goneEntry := procreap.NewEntry("attn-pi", pid, pid, cmd.Args)
	goneEntry.PID = 0
	goneEntry.PGID = 0
	if err := procreap.WriteEntry(gonePath, goneEntry); err != nil {
		t.Fatalf("write stale record: %v", err)
	}

	d := NewForTesting(filepath.Join(dataDir, "test.sock"))
	d.reapStrandedPluginRuntimes()

	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatalf("stranded runtime %d survived the reap", pid)
	}
	for _, path := range []string{livePath, gonePath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("registry record %s survived the reap: %v", filepath.Base(path), err)
		}
	}
}
