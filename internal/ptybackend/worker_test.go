package ptybackend

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/victorarias/attn/internal/ptyhost"
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

func mustHostSocketPath(t *testing.T, backend *WorkerBackend) string {
	t.Helper()
	path, err := ptyhost.SocketPath(backend.cfg.DataRoot, backend.cfg.DaemonInstanceID, "host-1")
	if err != nil {
		t.Fatalf("ptyhost.SocketPath() error: %v", err)
	}
	return path
}

func TestWorkerBackend_Recover_QuarantinesOwnershipMismatch(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewSharedHost() error: %v", err)
	}

	socketPath := mustHostSocketPath(t, backend)

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
}

func TestWorkerBackend_Recover_RejectsUnexpectedSocketPath(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewSharedHost() error: %v", err)
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

func TestWorkerBackend_SessionLikelyAlive_UsesValidatedRegistry(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewSharedHost() error: %v", err)
	}

	registryPath := filepath.Join(backend.registryDir(), "sess-1.json")
	socketPath := mustHostSocketPath(t, backend)
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
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewSharedHost() error: %v", err)
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
