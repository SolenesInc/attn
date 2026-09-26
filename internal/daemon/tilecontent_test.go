package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func setupMarkdownWorkspace(t *testing.T) (*Daemon, *wsClient, string) {
	t.Helper()
	return setupMarkdownWorkspaceOn(t, NewForTesting(filepath.Join(t.TempDir(), "test.sock")))
}

func setupMarkdownWorkspaceOn(t *testing.T, d *Daemon) (*Daemon, *wsClient, string) {
	t.Helper()
	client := newWorkspaceProtocolTestClient()
	workspaceID := "workspace-md"
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Markdown",
		Directory: t.TempDir(),
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr("pane-1"),
		SessionID:   "session-1",
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, "pane-1", true)
	return d, client, workspaceID
}

func TestOpenMarkdownRejectsBareOpenAfterRemoteSessionSelection(t *testing.T) {
	d, client, workspaceID := setupMarkdownWorkspace(t)
	d.setSelectedSession("session-1")
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil, nil)
	endpoint, err := d.hubManager.AddEndpoint("remote", "remote.example.test", "")
	if err != nil {
		t.Fatalf("add endpoint: %v", err)
	}
	d.hubManager.ReservePendingSessionRoute(endpoint.ID, "session-remote")
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"session_selected","id":"session-remote"}`))
	if got := d.currentlySelectedSession(); got != "session-remote" {
		t.Fatalf("selected session = %q, want remote session", got)
	}

	file := filepath.Join(t.TempDir(), "remote.md")
	if err := os.WriteFile(file, []byte("# Remote"), 0o644); err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	go d.handleOpenMarkdown(serverConn, &protocol.OpenMarkdownMessage{
		Cmd:  protocol.CmdOpenMarkdown,
		Path: file,
	})

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "no workspace found for session session-remote") {
		t.Fatalf("response = %+v, want explicit remote-selection error", resp)
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	if snapshot == nil {
		t.Fatal("local workspace layout missing")
	}
	if leaves := workspacelayout.TileLeaves(snapshot.Layout); len(leaves) != 0 {
		t.Fatalf("local workspace tiles = %+v, want no stale local dock", leaves)
	}
}
