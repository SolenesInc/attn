package ptybackend

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/ptyworker"
)

type fakeSharedHost struct {
	listener net.Listener
	token    string

	mu             sync.Mutex
	inputs         []string
	dropAfterInput bool
	connections    []net.Conn
}

func startFakeSharedHost(t *testing.T, socketPath, token string) *fakeSharedHost {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	host := &fakeSharedHost{listener: listener, token: token}
	t.Cleanup(host.stop)
	go host.accept()
	return host
}

func (h *fakeSharedHost) accept() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			return
		}
		h.mu.Lock()
		h.connections = append(h.connections, conn)
		h.mu.Unlock()
		go h.serve(conn)
	}
}

func (h *fakeSharedHost) serve(conn net.Conn) {
	defer conn.Close()
	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)
	for {
		var req ptyworker.RequestEnvelope
		if err := dec.Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case ptyworker.MethodHello:
			var hello ptyworker.HelloParams
			_ = json.Unmarshal(req.Params, &hello)
			if hello.ControlToken != h.token {
				_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, Error: &ptyworker.RPCError{Code: "unauthorized", Message: "daemon identity or control token mismatch"}})
				return
			}
			result, _ := json.Marshal(ptyworker.HelloResult{RPCMajor: ptyworker.RPCMajor, RPCMinor: 7, DaemonInstanceID: hello.DaemonInstanceID, SessionID: hello.SessionID})
			_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: result})
		case ptyworker.MethodInput:
			var input ptyworker.InputParams
			_ = json.Unmarshal(req.Params, &input)
			h.mu.Lock()
			h.inputs = append(h.inputs, input.Data)
			drop := h.dropAfterInput
			h.mu.Unlock()
			if drop {
				return
			}
			_ = enc.Encode(ptyworker.ResponseEnvelope{Type: "res", ID: req.ID, OK: true, Result: json.RawMessage(`{"ok":true}`)})
		}
	}
}

func (h *fakeSharedHost) dropConnections() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, conn := range h.connections {
		_ = conn.Close()
	}
	h.connections = nil
}

func (h *fakeSharedHost) receivedInputs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.inputs...)
}

func (h *fakeSharedHost) stop() {
	_ = h.listener.Close()
	h.dropConnections()
}

func newFakeSharedBackend(t *testing.T) (*WorkerBackend, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "attn-fake-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-fake-host", BinaryPath: filepath.Join(root, "missing-host"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend, filepath.Join(root, "host.sock")
}

func addFakeSharedSession(backend *WorkerBackend, id, socketPath, token string) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.sessions[id] = &workerSession{SessionID: id, SocketPath: socketPath, ControlToken: token}
}

func TestSharedHostReplacementNeverReusesStaleCredentials(t *testing.T) {
	backend, socketPath := newFakeSharedBackend(t)
	first := startFakeSharedHost(t, socketPath, "first-token")
	addFakeSharedSession(backend, "before-replacement", socketPath, "first-token")
	if err := backend.Input(context.Background(), "before-replacement", []byte("a")); err != nil {
		t.Fatal(err)
	}
	first.stop()
	_ = os.Remove(socketPath)

	second := startFakeSharedHost(t, socketPath, "second-token")
	addFakeSharedSession(backend, "after-replacement", socketPath, "second-token")
	if err := backend.Input(context.Background(), "after-replacement", []byte("b")); err != nil {
		t.Fatalf("input to the replacement host used stale credentials: %v", err)
	}
	if err := backend.Input(context.Background(), "before-replacement", []byte("c")); err == nil {
		t.Fatal("a session of the retired host authenticated against its replacement")
	}
	if got := second.receivedInputs(); len(got) != 1 {
		t.Fatalf("replacement host inputs = %v, want only the new session's input", got)
	}
}

func TestSharedHostInputIsNeverResentAfterDelivery(t *testing.T) {
	backend, socketPath := newFakeSharedBackend(t)
	host := startFakeSharedHost(t, socketPath, "token")
	addFakeSharedSession(backend, "session", socketPath, "token")
	if err := backend.Input(context.Background(), "session", []byte("warm")); err != nil {
		t.Fatal(err)
	}

	host.mu.Lock()
	host.dropAfterInput = true
	host.mu.Unlock()
	if err := backend.Input(context.Background(), "session", []byte("delivered-once")); err == nil {
		t.Fatal("input without a response reported success")
	}
	if got := host.receivedInputs(); len(got) != 2 {
		t.Fatalf("host received %d inputs, want the delivered keystroke exactly once: %v", len(got), got)
	}
}

func TestSharedHostInputRetriesOnlyUndeliveredRequests(t *testing.T) {
	backend, socketPath := newFakeSharedBackend(t)
	host := startFakeSharedHost(t, socketPath, "token")
	addFakeSharedSession(backend, "session", socketPath, "token")
	if err := backend.Input(context.Background(), "session", []byte("warm")); err != nil {
		t.Fatal(err)
	}
	host.dropConnections()
	if err := backend.Input(context.Background(), "session", []byte("after-stale-connection")); err != nil {
		t.Fatalf("input over a stale cached connection: %v", err)
	}
	if got := host.receivedInputs(); len(got) != 2 {
		t.Fatalf("host received %d inputs, want each keystroke exactly once: %v", len(got), got)
	}
}
