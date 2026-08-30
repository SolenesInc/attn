package ptybackend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ptyworker"
)

func TestWorkerBackend_UpgradeKeepsTheSessionAlive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping worker integration test in short mode")
	}
	if os.Getenv("ATTN_RUN_WORKER_INTEGRATION") != "1" {
		t.Skip("set ATTN_RUN_WORKER_INTEGRATION=1 to run worker integration test")
	}

	binary := buildAttnBinary(t)
	root, err := os.MkdirTemp("/tmp", "attn-worker-upgrade-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	if os.Getenv("ATTN_KEEP_ROOT") == "" {
		defer os.RemoveAll(root)
	} else {
		t.Logf("data root: %s", root)
	}
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       binary,
		Logf:             func(format string, args ...any) { t.Logf(format, args...) },
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	const sessionID = "worker-upgrade-1"
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID:    sessionID,
		CWD:   t.TempDir(),
		Agent: "shell",
		Label: "worker-upgrade",
		Cols:  80,
		Rows:  24,
	}); err != nil {
		t.Skipf("worker spawn unavailable in this environment: %v", err)
	}
	defer func() { _ = backend.Remove(context.Background(), sessionID) }()

	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry, err := waitForRegistryEntry(registryPath, 10*time.Second)
	if err != nil {
		t.Fatalf("registry entry never appeared: %v", err)
	}
	workerPID, childPID := entry.WorkerPID, entry.ChildPID

	waitForScreen(t, backend, sessionID, "__BEFORE_UPGRADE__",
		"printf '__BEFORE_UPGRADE__\\n'\n")

	result, err := backend.upgrade(context.Background(), sessionID, binary)
	if err != nil {
		t.Fatalf("Upgrade() error: %v", err)
	}
	if result.ChildPID != childPID {
		t.Errorf("upgrade reports child pid %d, want %d — the agent must not be restarted", result.ChildPID, childPID)
	}
	if result.DumpBytes == 0 {
		t.Error("the upgrade carried an empty screen dump")
	}

	snap, err := backend.ScreenSnapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ScreenSnapshot() right after the upgrade: %v", err)
	}
	if !snap.Running {
		t.Fatal("the session is not running after the upgrade")
	}
	if snap.Screen == nil || !strings.Contains(snap.Screen.Text, "__BEFORE_UPGRADE__") {
		text := ""
		if snap.Screen != nil {
			text = snap.Screen.Text
		}
		t.Errorf("the screen did not survive the upgrade; got %q", text)
	}

	after, err := waitForRegistryEntry(registryPath, 10*time.Second)
	if err != nil {
		t.Fatalf("registry entry missing after the upgrade: %v", err)
	}
	if after.WorkerPID != workerPID {
		t.Errorf("worker pid = %d after the upgrade, want %d", after.WorkerPID, workerPID)
	}
	if after.ChildPID != childPID {
		t.Errorf("child pid = %d after the upgrade, want %d", after.ChildPID, childPID)
	}

	waitForScreen(t, backend, sessionID, "__AFTER_UPGRADE__",
		"printf '__AFTER_UPGRADE__\\n'\n")

	// A leftover handoff file makes the next reader think an upgrade is pending.
	jsonPath, dumpPath := ptyworker.HandoffPaths(filepath.Join(backend.registryDir(), sessionID+".json"), sessionID)
	for _, path := range []string{jsonPath, dumpPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("handoff file %s still exists after the adopt (err=%v)", path, err)
		}
	}
}

func TestWorkerBackend_UpgradeRefusesABinaryThatIsNotOne(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping worker integration test in short mode")
	}
	if os.Getenv("ATTN_RUN_WORKER_INTEGRATION") != "1" {
		t.Skip("set ATTN_RUN_WORKER_INTEGRATION=1 to run worker integration test")
	}

	binary := buildAttnBinary(t)
	root, err := os.MkdirTemp("/tmp", "attn-worker-upgrade-bad-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	if os.Getenv("ATTN_KEEP_ROOT") == "" {
		defer os.RemoveAll(root)
	} else {
		t.Logf("data root: %s", root)
	}
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       binary,
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	const sessionID = "worker-upgrade-bad"
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: sessionID, CWD: t.TempDir(), Agent: "shell", Cols: 80, Rows: 24,
	}); err != nil {
		t.Skipf("worker spawn unavailable in this environment: %v", err)
	}
	defer func() { _ = backend.Remove(context.Background(), sessionID) }()

	// The worker validates before it captures; capturing is the point of no return.
	if _, err := backend.upgrade(context.Background(), sessionID, "/nonexistent/attn"); err == nil {
		t.Fatal("Upgrade() accepted a path with no binary at it")
	}
	snap, err := backend.ScreenSnapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ScreenSnapshot() after a refused upgrade: %v", err)
	}
	if !snap.Running {
		t.Fatal("a refused upgrade killed the session")
	}
}

// Waits on the rendered screen, not the byte stream: the screen is what has to
// survive the swap.
func waitForScreen(t *testing.T, backend *WorkerBackend, sessionID, marker, input string) {
	t.Helper()
	if err := backend.Input(context.Background(), sessionID, []byte(input)); err != nil {
		t.Fatalf("Input(%q) error: %v", input, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		snap, err := backend.ScreenSnapshot(context.Background(), sessionID)
		if err == nil && snap.Screen != nil {
			last = snap.Screen.Text
			if strings.Contains(last, marker) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q on the screen; last screen: %q", marker, last)
}
