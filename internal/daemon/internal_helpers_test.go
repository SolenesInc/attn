package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/testworld"
)

func writeCodexInteractiveRollout(t *testing.T, codexHome, nativeID, cwd string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "05", "17")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("rollout-%s-%s.jsonl", at.UTC().Format("2006-01-02T15-04-05"), nativeID))
	line := fmt.Sprintf(
		`{"timestamp":"%s","type":"session_meta","payload":{"id":"%s","timestamp":"%s","cwd":"%s","source":"cli"}}`+"\n",
		at.UTC().Format(time.RFC3339Nano), nativeID, at.UTC().Format(time.RFC3339Nano), cwd,
	)
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("chtimes rollout: %v", err)
	}
	return path
}

func newEnrolledDaemon(t *testing.T, homeDaemonID string) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	id, err := enrollment.EnsureDaemonID(d.dataRoot)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	d.daemonInstanceID = id
	if err := d.ensureEnrollment(); err != nil {
		t.Fatalf("ensureEnrollment: %v", err)
	}
	if homeDaemonID != "" {
		if _, err := enrollment.Enroll(d.dataRoot, homeDaemonID); err != nil {
			t.Fatalf("Enroll: %v", err)
		}
	}
	return d
}

func startPluginPipe(t *testing.T, d *Daemon, name string, surfaces []string) (net.Conn, <-chan struct{}) {
	return startPluginPipeGeneration(t, d, name, surfaces, 1)
}

func startPluginPipeGeneration(t *testing.T, d *Daemon, name string, surfaces []string, generation uint64) (net.Conn, <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	sendPluginHelloWithGeneration(t, clientConn, name, surfaces, generation)
	helloResp := decodeJSONRPCMessage(t, clientConn)
	if helloResp.Error != nil {
		t.Fatalf("hello error = %#v, want nil", helloResp.Error)
	}
	return clientConn, done
}

func sendPluginHelloWithGeneration(t *testing.T, conn net.Conn, name string, surfaces []string, generation uint64) {
	t.Helper()
	params, err := json.Marshal(pluginHelloParams{
		Name:           name,
		Version:        "0.1.0",
		AttnAPIVersion: pluginAPIVersion,
		Generation:     generation,
		Surfaces:       surfaces,
	})
	if err != nil {
		t.Fatalf("marshal hello params: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "hello",
		Params:  params,
	}); err != nil {
		t.Fatalf("encode hello: %v", err)
	}
}

func decodeJSONRPCMessage(t *testing.T, conn net.Conn) jsonRPCMessage {
	t.Helper()
	var frame []byte
	var b [1]byte
	for b[0] != '\n' {
		if _, err := io.ReadFull(conn, b[:]); err != nil {
			t.Fatalf("read JSON-RPC frame: %v", err)
		}
		frame = append(frame, b[0])
	}
	var msg jsonRPCMessage
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatalf("decode JSON-RPC message: %v", err)
	}
	return msg
}

func newDaemonForTest(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}

func mdAnchor(startLine, endLine, start int, exact string) *protocol.MarkdownAnnotationAnchor {
	return &protocol.MarkdownAnnotationAnchor{
		BlockID:   "b",
		StartLine: startLine,
		EndLine:   endLine,
		Start:     start,
		End:       start + len(exact),
		Exact:     exact,
	}
}

func fileAnnotationSource(path string) annotationDocumentSource {
	return annotationDocumentSource{kind: annotationSourceFile, path: path}
}

func factsOf(t *testing.T, d *Daemon) []store.BusEvent {
	t.Helper()
	events, err := d.store.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	return events
}

func docFacts(t *testing.T, d *Daemon, name string) []store.BusEvent {
	t.Helper()
	var out []store.BusEvent
	for _, e := range factsOf(t, d) {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

var shippedSessionInputSubmitDelay = sessionInputSubmitDelay

func TestMain(m *testing.M) {
	fakeagent.Main()
	if os.Getenv("ATTN_PLUGIN_HELPER") == "1" {
		os.Exit(m.Run())
	}
	sessionInputSubmitDelay = 0
	os.Exit(testworld.Main(m,
		"ATTN_PTY_BACKEND=embedded",
		"ATTN_PTY_SKIP_STARTUP_PROBE=1",
		"ATTN_CLIENT_TOKEN=daemon-test-client-token",
		"ATTN_MOCK_GH_URL=http://127.0.0.1:1",
		"ATTN_MOCK_GH_TOKEN=",
	))
}

func waitForSocket(t *testing.T, sockPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sockPath, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s not ready after %v", sockPath, timeout)
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "attn-")
	if err != nil {
		t.Fatalf("MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
