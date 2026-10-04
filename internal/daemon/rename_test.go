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

func TestRenameSessionOverTheUnixSocketTravelsToTheSessionOwner(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	dir := t.TempDir()
	d.store.Add(&protocol.Session{ID: "s1", Label: "local", Agent: protocol.SessionAgentClaude, Directory: dir, ProfileID: defaultProfileID(t, d.store)})
	endpoint, err := d.store.AddEndpoint("remote", "remote.example.test", "")
	if err != nil {
		t.Fatalf("add endpoint: %v", err)
	}
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil, nil)
	d.hubManager.ReservePendingSessionRoute(endpoint.ID, "s-remote")

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go d.handleConnection(serverConn)
	if err := json.NewEncoder(clientConn).Encode(protocol.RenameSessionMessage{
		Cmd: protocol.CmdRenameSession, SessionID: "s-remote", Label: "pi resume support",
	}); err != nil {
		t.Fatalf("send command: %v", err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "rename session s-remote on the endpoint owning it") {
		t.Fatalf("response = %+v, want it routed to the owning endpoint, not session not found", resp)
	}
	if got := d.store.Get("s1"); got.Label != "local" {
		t.Fatalf("local session label = %q, want the hub's own session untouched", got.Label)
	}
}
