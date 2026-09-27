package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestClient_ListIncludesProfiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("/tmp", "attn-client-")
	if err != nil {
		t.Fatalf("MkdirTemp error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	sockPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		conn.Read(buf)

		resp := protocol.Response{
			Ok: true,
			Sessions: []protocol.Session{
				{ID: "1", Label: "one", State: protocol.SessionStateWaitingInput},
			},
			Profiles: []protocol.Profile{
				{ID: "profile-default", Name: "Default"},
			},
		}
		json.NewEncoder(conn).Encode(resp)
	}()

	c := New(sockPath)
	result, err := c.List("")
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(result.Sessions))
	}
	if len(result.Profiles) != 1 || result.Profiles[0].Name != "Default" {
		t.Fatalf("profiles = %+v, want the Default profile", result.Profiles)
	}
}
