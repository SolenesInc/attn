package ptybackend

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/ptyworker"
)

func newWorkerBackendTestRoot(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = ""
	}
	root, err := os.MkdirTemp(base, "attnwb-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func mustWorkerSocketPath(t *testing.T, backend *WorkerBackend, sessionID string) string {
	t.Helper()
	path, err := backend.expectedSocketPath(sessionID)
	if err != nil {
		t.Fatalf("expectedSocketPath(%q) error: %v", sessionID, err)
	}
	return path
}

func TestWorkerBackend_Recover_QuarantinesOwnershipMismatch(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	socketPath := mustWorkerSocketPath(t, backend, "sess-1")
	if err := os.WriteFile(socketPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("WriteFile(stale socket) error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-1.json")
	entry := ptyworker.NewRegistryEntry(
		"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"sess-1",
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"shell",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("failed = %d, want 1", report.Failed)
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry file should be moved to quarantine, stat err=%v", err)
	}
	files, err := filepath.Glob(filepath.Join(backend.quarantineDir(), "sess-1.json.*"))
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("quarantine files = %d, want 1", len(files))
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("owned socket should be removed for ownership mismatch, stat err=%v", err)
	}
}

func TestWorkerBackend_Recover_ReclaimsStaleOwnershipMismatch(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-reclaim"
	socketPath := mustWorkerSocketPath(t, backend, sessionID)
	stopServer := startFakeWorkerRPCServer(
		t,
		"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		sessionID,
		"tok-reclaim",
		socketPath,
		"shell",
		t.TempDir(),
	)
	defer stopServer()

	oldWorker := exec.Command("sleep", "0.3")
	if err := oldWorker.Start(); err != nil {
		t.Fatalf("start old worker: %v", err)
	}
	go func() { _ = oldWorker.Wait() }()

	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry := ptyworker.NewRegistryEntry(
		"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		sessionID,
		oldWorker.Process.Pid,
		os.Getpid(),
		socketPath,
		"shell",
		t.TempDir(),
		"tok-reclaim",
	)
	entry.OwnerPID = 2147483647
	entry.OwnerStartedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	entry.OwnerNonce = "owner-old"
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if procreap.ProcessAlive(oldWorker.Process.Pid) {
		t.Fatal("reclaim returned while the old worker was still running")
	}
	if report.Pruned != 1 {
		t.Fatalf("pruned = %d, want 1", report.Pruned)
	}
	if report.Failed != 0 {
		t.Fatalf("failed = %d, want 0", report.Failed)
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry should be removed after stale-owner reclaim, stat err=%v", err)
	}
	files, err := filepath.Glob(filepath.Join(backend.quarantineDir(), sessionID+".json.*"))
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("quarantine files = %d, want 0", len(files))
	}
}

func TestWorkerBackend_Recover_PreservesLiveOwnerMismatch(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-live-owner"
	socketPath := mustWorkerSocketPath(t, backend, sessionID)
	if err := os.WriteFile(socketPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("WriteFile(stale socket) error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry := ptyworker.NewRegistryEntry(
		"d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		sessionID,
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"shell",
		t.TempDir(),
		"tok-live",
	)
	entry.OwnerPID = os.Getpid()
	entry.OwnerStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	entry.OwnerNonce = "different-owner"
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("failed = %d, want 1", report.Failed)
	}
	files, err := filepath.Glob(filepath.Join(backend.quarantineDir(), sessionID+".json.ownership_mismatch.*"))
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("quarantine files = %d, want 1", len(files))
	}
}

func TestWorkerBackend_Recover_RejectsUnexpectedSocketPath(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	externalSocketPath := filepath.Join(t.TempDir(), "external.sock")
	if err := os.WriteFile(externalSocketPath, []byte("placeholder"), 0600); err != nil {
		t.Fatalf("WriteFile(external socket) error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-unsafe.json")
	entry := ptyworker.NewRegistryEntry(
		"d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sess-unsafe",
		os.Getpid(),
		os.Getpid(),
		externalSocketPath,
		"shell",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("failed = %d, want 1", report.Failed)
	}
	if _, err := os.Stat(externalSocketPath); err != nil {
		t.Fatalf("external socket path should not be removed, stat err=%v", err)
	}

	files, err := filepath.Glob(filepath.Join(backend.quarantineDir(), "sess-unsafe.json.socket_path_mismatch.*"))
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("quarantine files = %d, want 1", len(files))
	}
}

func TestWorkerBackend_Recover_AcceptsLegacySocketPath(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-legacy"
	socketPath, err := backend.legacyExpectedSocketPath(sessionID)
	if err != nil {
		t.Fatalf("legacyExpectedSocketPath() error: %v", err)
	}
	registryPath := filepath.Join(backend.registryDir(), sessionID+".json")
	entry := ptyworker.NewRegistryEntry(
		backend.cfg.DaemonInstanceID,
		sessionID,
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"codex",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}
	stopServer := startFakeWorkerRPCServer(t, backend.cfg.DaemonInstanceID, sessionID, "tok", socketPath, "codex", t.TempDir())
	defer stopServer()

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Recovered != 1 {
		t.Fatalf("recovered = %d, want 1", report.Recovered)
	}
	if report.Failed != 0 {
		t.Fatalf("failed = %d, want 0", report.Failed)
	}
}

func TestWorkerBackend_Recover_RestoresSocketMismatchQuarantine(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	sessionID := "sess-quarantine-restore"
	socketPath, err := backend.legacyExpectedSocketPath(sessionID)
	if err != nil {
		t.Fatalf("legacyExpectedSocketPath() error: %v", err)
	}

	quarantinePath := filepath.Join(backend.quarantineDir(), sessionID+".json.socket_path_mismatch.123")
	entry := ptyworker.NewRegistryEntry(
		backend.cfg.DaemonInstanceID,
		sessionID,
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"codex",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(quarantinePath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic(quarantine) error: %v", err)
	}
	stopServer := startFakeWorkerRPCServer(t, backend.cfg.DaemonInstanceID, sessionID, "tok", socketPath, "codex", t.TempDir())
	defer stopServer()

	report, err := backend.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error: %v", err)
	}
	if report.Recovered != 1 {
		t.Fatalf("recovered = %d, want 1", report.Recovered)
	}
	if _, err := os.Stat(filepath.Join(backend.registryDir(), sessionID+".json")); err != nil {
		t.Fatalf("restored registry missing: %v", err)
	}
	if _, err := os.Stat(quarantinePath); err == nil {
		t.Fatal("quarantine file should have been moved")
	}
}

func TestWorkerBackend_SessionLikelyAlive_UsesValidatedRegistry(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-1.json")
	socketPath := mustWorkerSocketPath(t, backend, "sess-1")
	entry := ptyworker.NewRegistryEntry(
		"d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sess-1",
		os.Getpid(),
		os.Getpid(),
		socketPath,
		"codex",
		t.TempDir(),
		"tok",
	)
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() error: %v", err)
	}
	stopServer := startFakeWorkerRPCServer(t, backend.cfg.DaemonInstanceID, "sess-1", "tok", socketPath, "codex", t.TempDir())
	defer stopServer()

	alive, err := backend.SessionLikelyAlive(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("SessionLikelyAlive() error: %v", err)
	}
	if !alive {
		t.Fatal("SessionLikelyAlive() = false, want true")
	}

	entry.SocketPath = filepath.Join(t.TempDir(), "unexpected.sock")
	if err := ptyworker.WriteRegistryAtomic(registryPath, entry); err != nil {
		t.Fatalf("WriteRegistryAtomic() mismatch error: %v", err)
	}
	alive, err = backend.SessionLikelyAlive(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("SessionLikelyAlive() mismatch error: %v", err)
	}
	if alive {
		t.Fatal("SessionLikelyAlive() should be false for mismatched socket path")
	}
}

func TestWorkerBackend_SessionLikelyAlive_MalformedRegistryReturnsError(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-bad.json")
	if err := os.WriteFile(registryPath, []byte("{not-json"), 0600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	alive, err := backend.SessionLikelyAlive(context.Background(), "sess-bad")
	if alive {
		t.Fatal("SessionLikelyAlive() = true, want false")
	}
	if err == nil {
		t.Fatal("SessionLikelyAlive() error = nil, want non-nil for malformed registry")
	}
}

func TestWorkerBackend_Spawn_CleansUpUnreadyWorkerProcess(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	pidFile := filepath.Join(root, "worker.pid")
	scriptPath := filepath.Join(root, "fake-worker.sh")
	script := "#!/bin/sh\n" +
		"echo $$ > \"$ATTN_TEST_PID_FILE\"\n" +
		"sleep 60\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(fake worker script) error: %v", err)
	}

	t.Setenv("ATTN_TEST_PID_FILE", pidFile)
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       scriptPath,
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	spawnDone := make(chan error, 1)
	go func() {
		spawnDone <- backend.Spawn(ctx, SpawnOptions{
			ID:    "sess-timeout",
			Agent: "codex",
			CWD:   root,
			Cols:  80,
			Rows:  24,
		})
	}()

	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-spawnDone:
			cancel()
			t.Fatalf("Spawn() returned before worker started (pid file missing): %v", err)
		default:
		}
		data, readErr := os.ReadFile(pidFile)
		if readErr != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		parsedPID, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil {
			t.Fatalf("Atoi(pid file) error: %v", parseErr)
		}
		pid = parsedPID
		break
	}
	if pid == 0 {
		t.Fatalf("timed out waiting for fake worker pid file at %s", pidFile)
	}

	cancel()
	select {
	case err := <-spawnDone:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("Spawn() error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Spawn() did not return after context cancellation")
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("worker pid %d still alive after spawn failure cleanup", pid)
}

func startFakeWorkerRPCServer(
	t *testing.T,
	daemonInstanceID string,
	sessionID string,
	controlToken string,
	socketPath string,
	agent string,
	cwd string,
) func() {
	t.Helper()

	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen fake worker socket: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup

	handleConn := func(conn net.Conn, infoCalls *atomic.Int64) {
		defer conn.Close()
		enc := json.NewEncoder(conn)
		dec := json.NewDecoder(conn)

		for {
			var req ptyworker.RequestEnvelope
			if err := dec.Decode(&req); err != nil {
				return
			}
			switch req.Method {
			case ptyworker.MethodHello:
				params := ptyworker.HelloParams{}
				_ = json.Unmarshal(req.Params, &params)
				if params.DaemonInstanceID != daemonInstanceID || params.ControlToken != controlToken {
					_ = enc.Encode(ptyworker.ResponseEnvelope{
						Type:  "res",
						ID:    req.ID,
						OK:    false,
						Error: &ptyworker.RPCError{Code: ptyworker.ErrUnauthorized, Message: "unauthorized"},
					})
					return
				}
				result, _ := json.Marshal(ptyworker.HelloResult{
					WorkerVersion:    "test-worker",
					RPCMajor:         ptyworker.RPCMajor,
					RPCMinor:         ptyworker.RPCMinor,
					DaemonInstanceID: daemonInstanceID,
					SessionID:        sessionID,
				})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			case ptyworker.MethodInfo:
				infoCalls.Add(1)
				result, _ := json.Marshal(ptyworker.InfoResult{
					Running:   true,
					Agent:     agent,
					CWD:       cwd,
					Cols:      80,
					Rows:      24,
					WorkerPID: os.Getpid(),
					ChildPID:  os.Getpid(),
					LastSeq:   1,
					State:     "working",
				})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			case ptyworker.MethodHealth:
				result, _ := json.Marshal(map[string]any{"ok": true, "running": true})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			case ptyworker.MethodWatch:
				_ = enc.Encode(ptyworker.ResponseEnvelope{
					Type: "res",
					ID:   req.ID,
					OK:   false,
					Error: &ptyworker.RPCError{
						Code:    ptyworker.ErrUnsupportedVersion,
						Message: "watch unsupported in fake server",
					},
				})
			default:
				result, _ := json.Marshal(map[string]any{"ok": true})
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
			}
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		infoCalls := &atomic.Int64{}
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				handleConn(c, infoCalls)
			}(conn)
		}
	}()

	return func() {
		close(done)
		_ = listener.Close()
		wg.Wait()
		_ = os.Remove(socketPath)
	}
}
