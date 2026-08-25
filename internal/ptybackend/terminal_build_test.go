package ptybackend

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ptyworker"
)

func serveWorkerHello(listener net.Listener, daemonInstanceID, sessionID string, format func() string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		defer wg.Wait()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				enc := json.NewEncoder(conn)
				dec := json.NewDecoder(conn)
				for {
					var req ptyworker.RequestEnvelope
					if err := dec.Decode(&req); err != nil {
						return
					}
					var result []byte
					if req.Method == ptyworker.MethodHello {
						result, _ = json.Marshal(ptyworker.HelloResult{
							WorkerVersion:    "test-worker",
							RPCMajor:         ptyworker.RPCMajor,
							RPCMinor:         ptyworker.RPCMinor,
							DaemonInstanceID: daemonInstanceID,
							SessionID:        sessionID,
							SnapshotFormat:   format(),
						})
					} else {
						result, _ = json.Marshal(map[string]any{"ok": true})
					}
					_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
				}
			}()
		}
	}()
	return done
}

type terminalBuildHarness struct {
	backend   *WorkerBackend
	sessionID string
	listener  net.Listener
	served    <-chan struct{}

	mu              sync.Mutex
	format          string
	reported        []string
	reportedFormats []string
}

func newTerminalBuildHarness(t *testing.T, sessionID, format string) *terminalBuildHarness {
	t.Helper()
	root := newWorkerBackendTestRoot(t)
	h := &terminalBuildHarness{sessionID: sessionID, format: format}

	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
		OnTerminalBuild: func(id, format string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.reported = append(h.reported, id)
			h.reportedFormats = append(h.reportedFormats, format)
		},
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}
	h.backend = backend

	socketPath := filepath.Join(root, "worker.sock")
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen(unix) error: %v", err)
	}
	h.listener = listener
	h.served = serveWorkerHello(listener, backend.cfg.DaemonInstanceID, sessionID, func() string {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.format
	})
	t.Cleanup(func() {
		// The backend's end of the control connection first: closing the listener does
		// not close a connection it already accepted, so the fake's reader would block.
		backend.mu.RLock()
		session := backend.sessions[sessionID]
		backend.mu.RUnlock()
		if session != nil {
			backend.closePersistentControlConn(session, "test_done")
		}
		_ = listener.Close()
		_ = os.Remove(socketPath)
		select {
		case <-h.served:
		case <-time.After(2 * time.Second):
			t.Error("timed out waiting for the fake worker to exit")
		}
	})

	backend.mu.Lock()
	backend.sessions[sessionID] = &workerSession{
		SessionID:    sessionID,
		SocketPath:   socketPath,
		RegistryPath: filepath.Join(backend.registryDir(), sessionID+".json"),
		ControlToken: "tok",
	}
	backend.mu.Unlock()
	return h
}

func (h *terminalBuildHarness) handshake(t *testing.T) {
	t.Helper()
	h.backend.mu.RLock()
	session := h.backend.sessions[h.sessionID]
	h.backend.mu.RUnlock()
	if session != nil {
		h.backend.closePersistentControlConn(session, "test_next_handshake")
	}
	if err := h.backend.Input(context.Background(), h.sessionID, []byte("a")); err != nil {
		t.Fatalf("Input() error: %v", err)
	}
}

func (h *terminalBuildHarness) setFormat(format string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.format = format
}

func (h *terminalBuildHarness) reports() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.reported...)
}

func (h *terminalBuildHarness) reportedFormatsCopy() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.reportedFormats...)
}

func TestWorkerBackendRecordsTheFormatItsWorkerReports(t *testing.T) {
	h := newTerminalBuildHarness(t, "sess-terminal-build", "0123456789ab")
	h.handshake(t)

	format, known := h.backend.SessionTerminalBuild(h.sessionID)
	if !known || format != "0123456789ab" {
		t.Fatalf("SessionTerminalBuild() = (%q, %v), want (\"0123456789ab\", true)", format, known)
	}
	if got := h.reports(); len(got) != 1 || got[0] != h.sessionID {
		t.Fatalf("OnTerminalBuild fired %v, want exactly one call for %s", got, h.sessionID)
	}
	if got := h.reportedFormatsCopy(); len(got) != 1 || got[0] != "0123456789ab" {
		t.Fatalf("OnTerminalBuild carried %v, want [\"0123456789ab\"]", got)
	}
}

func TestWorkerBackendReportsOnlyWhenTheFormatMoves(t *testing.T) {
	h := newTerminalBuildHarness(t, "sess-terminal-build-repeat", "0123456789ab")
	h.handshake(t)
	h.handshake(t)
	h.handshake(t)

	if got := h.reports(); len(got) != 1 {
		t.Fatalf("OnTerminalBuild fired %v across three handshakes, want one", got)
	}

	h.setFormat("ba9876543210")
	h.handshake(t)

	if got := h.reports(); len(got) != 2 {
		t.Fatalf("OnTerminalBuild fired %v after the format changed, want two", got)
	}
	if format, _ := h.backend.SessionTerminalBuild(h.sessionID); format != "ba9876543210" {
		t.Fatalf("SessionTerminalBuild() = %q, want the format from the latest handshake", format)
	}
}

func TestWorkerBackendTreatsASilentWorkerAsAnAnswer(t *testing.T) {
	h := newTerminalBuildHarness(t, "sess-terminal-build-silent", "")
	h.handshake(t)

	format, known := h.backend.SessionTerminalBuild(h.sessionID)
	if !known || format != "" {
		t.Fatalf("SessionTerminalBuild() = (%q, %v), want (\"\", true)", format, known)
	}
	if got := h.reports(); len(got) != 1 {
		t.Fatalf("OnTerminalBuild fired %v, want exactly one call", got)
	}
}

// The lookup must not fall back to getSession's registry read + probe RPC: it
// runs on every session broadcast.
func TestWorkerBackendHasNoVerdictForAnUnknownSession(t *testing.T) {
	backend, err := NewWorker(WorkerBackendConfig{
		DataRoot:         newWorkerBackendTestRoot(t),
		DaemonInstanceID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BinaryPath:       "/bin/true",
	})
	if err != nil {
		t.Fatalf("NewWorker() error: %v", err)
	}
	if format, known := backend.SessionTerminalBuild("nobody"); known || format != "" {
		t.Fatalf("SessionTerminalBuild() = (%q, %v), want (\"\", false)", format, known)
	}
}
