package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
)

func TestPriorityOverTheUnixSocketReachesTheSessionOwner(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	endpoint, err := d.store.AddEndpoint("remote", "remote.example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil, nil)
	d.hubManager.ReservePendingSessionRoute(endpoint.ID, "s-remote")
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() { defer close(done); d.handleConnection(serverConn) }()
	if err := json.NewEncoder(clientConn).Encode(protocol.SetSessionPriorityMessage{Cmd: protocol.CmdSetSessionPriority, SessionID: "s-remote", Priority: true}); err != nil {
		t.Fatal(err)
	}
	var response protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	<-done
	if response.Ok || !strings.Contains(protocol.Deref(response.Error), "reach the endpoint owning session s-remote") {
		t.Fatalf("response = %+v, want the owning endpoint's connection failure", response)
	}
}
