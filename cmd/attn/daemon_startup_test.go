package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemonctl"
)

func TestDaemonPreflightHelperProcess(t *testing.T) {
	if os.Getenv("ATTN_DAEMON_PREFLIGHT_HELPER") == "" {
		return
	}
	signal, err := daemonctl.TakeStartupSignal()
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("ATTN_SOCKET_PATH", filepath.Join(t.TempDir(), "foreign", "attn.sock"))
	config.ReloadForTesting()
	_, err = daemonPreflight()
	signal.Failed(err)
	os.Exit(1)
}

func TestDaemonPreflightFailureReachesEnsureProcess(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonPreflightHelperProcess$")
	cmd.Env = append(os.Environ(),
		"ATTN_DAEMON_PREFLIGHT_HELPER=1",
		"ATTN_DAEMON_READY_FD=3",
	)
	cmd.ExtraFiles = []*os.File{writer}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	message, readErr := io.ReadAll(reader)
	waitErr := cmd.Wait()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if waitErr == nil {
		t.Fatal("preflight helper succeeded")
	}
	if got := string(message); !strings.Contains(got, "error:refusing to start daemon") {
		t.Fatalf("startup signal = %q, want daemon isolation error", got)
	}
}
