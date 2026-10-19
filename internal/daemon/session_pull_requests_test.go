package daemon

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func registerSessionForPRTest(t *testing.T, d *Daemon, id string) {
	t.Helper()
	injectTestSession(t, d, protocol.Session{ID: protocol.SessionID(id), Label: id, Directory: t.TempDir(), Agent: protocol.SessionAgentClaude})
}

func sendPRCommand(t *testing.T, d *Daemon, msg any) protocol.Response {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()
	if err := json.NewEncoder(clientConn).Encode(msg); err != nil {
		t.Fatalf("send command: %v", err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	<-done
	return resp
}

func newPRDaemonForTest(t *testing.T, sessionID string) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	stopDaemonBackground(t, d)
	registerSessionForPRTest(t, d, sessionID)
	return d
}
