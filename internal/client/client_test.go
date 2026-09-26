package client

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestClient_ConnectError_IncludesInstanceAndSocket(t *testing.T) {
	os.Unsetenv("ATTN_INSTANCE")
	sockPath := filepath.Join(t.TempDir(), "missing.sock")
	c := New(sockPath)
	err := c.Register("id", "label", "/tmp")
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}
	msg := err.Error()
	if !strings.Contains(msg, "instance=default") {
		t.Errorf("error missing instance=default: %q", msg)
	}
	if !strings.Contains(msg, "missing.sock") {
		t.Errorf("error missing socket path: %q", msg)
	}
}

func TestClient_ConnectError_HintsOtherInstanceWhenLive(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "attn-client-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	t.Setenv("HOME", tmp)
	t.Setenv("ATTN_INSTANCE", "dev")
	t.Setenv("ATTN_SOCKET_PATH", "")
	config.ReloadForTesting()

	defaultDir := filepath.Join(tmp, ".attn")
	if err := os.MkdirAll(defaultDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defaultSock := filepath.Join(defaultDir, "attn.sock")
	ln, err := net.Listen("unix", defaultSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	devSock := filepath.Join(tmp, ".attn-dev", "attn.sock")
	c := New(devSock)
	err = c.Register("id", "label", "/tmp")
	if err == nil {
		t.Fatal("expected error when dev daemon not running")
	}
	msg := err.Error()
	if !strings.Contains(msg, "instance=dev") {
		t.Errorf("error missing instance=dev: %q", msg)
	}
	if !strings.Contains(msg, "hint:") {
		t.Errorf("error missing cross-instance hint: %q", msg)
	}
	if !strings.Contains(msg, "default daemon is listening") {
		t.Errorf("error should hint about default daemon: %q", msg)
	}
}

func TestClient_SocketPath(t *testing.T) {
	config.SetBinaryName("attn")

	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	t.Setenv("ATTN_INSTANCE", "")
	t.Setenv("ATTN_SOCKET_PATH", "")
	t.Setenv("ATTN_CONFIG_PATH", filepath.Join(t.TempDir(), "missing-config.json"))
	config.ReloadForTesting()

	path := DefaultSocketPath()
	expected := filepath.Join(dataDir, "attn.sock")
	if path != expected {
		t.Errorf("DefaultSocketPath() = %q, want %q", path, expected)
	}
}
