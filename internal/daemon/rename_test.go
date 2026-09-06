package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func expectRenameResult(t *testing.T, client *wsClient) protocol.RenameResultMessage {
	t.Helper()
	select {
	case outbound := <-client.send:
		var result protocol.RenameResultMessage
		if err := json.Unmarshal(outbound.payload, &result); err != nil {
			t.Fatalf("decode rename_result: %v", err)
		}
		return result
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for rename_result")
		return protocol.RenameResultMessage{}
	}
}

func newRenameTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 4),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func TestDaemon_HandleRenameSession_PersistsAndSurvivesRespawn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	dir := t.TempDir()
	now := string(protocol.TimestampNow())
	addTestWorkspace(d, "workspace-s1", dir)
	d.store.Add(&protocol.Session{
		ID: "s1", Label: "original", Agent: protocol.SessionAgentClaude,
		Directory: dir, WorkspaceID: "workspace-s1",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.workspaces.associateSession("s1", "workspace-s1", "original")

	client := newRenameTestClient()
	d.handleRenameSession(client, &protocol.RenameSessionMessage{
		Cmd: protocol.CmdRenameSession, SessionID: "s1", Label: "renamed",
	})

	if res := expectRenameResult(t, client); !res.Success {
		t.Fatalf("rename_result success=false error=%q", protocol.Deref(res.Error))
	}
	if got := d.store.Get("s1"); got == nil || got.Label != "renamed" {
		t.Fatalf("stored label = %+v, want renamed", got)
	}

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "s1", Cwd: dir, Cols: 80, Rows: 24,
		Agent: "claude", WorkspaceID: "workspace-s1", Label: protocol.Ptr("original"),
	})
	if got := d.store.Get("s1"); got == nil || got.Label != "renamed" {
		t.Fatalf("label after stale respawn = %+v, want renamed", got)
	}
	if last, ok := backend.LastSpawn(); !ok || last.Label != "renamed" {
		t.Fatalf("spawn label = %q ok=%v, want renamed", last.Label, ok)
	}
}

func TestDaemon_HandleRenameWorkspace_PersistsTitle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	dir := t.TempDir()
	addTestWorkspace(d, "workspace-1", dir)

	client := newRenameTestClient()
	d.handleRenameWorkspace(client, &protocol.RenameWorkspaceMessage{
		Cmd: protocol.CmdRenameWorkspace, WorkspaceID: "workspace-1", Title: "Renamed",
	})

	if res := expectRenameResult(t, client); !res.Success {
		t.Fatalf("rename_result success=false error=%q", protocol.Deref(res.Error))
	}
	if got := d.store.GetWorkspace("workspace-1"); got == nil || got.Title != "Renamed" {
		t.Fatalf("stored title = %+v, want Renamed", got)
	}
	if snap, ok := d.workspaces.snapshot("workspace-1"); !ok || snap.Title != "Renamed" {
		t.Fatalf("registry title = %q ok=%v, want Renamed", snap.Title, ok)
	}
}

func TestDaemon_HandleRegisterWorkspace_PreservesRenamedTitleOnReRegister(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	dir := t.TempDir()
	client := newRenameTestClient()

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-1", Title: "derived-title", Directory: dir,
	})
	if got := d.store.GetWorkspace("workspace-1"); got == nil || got.Title != "derived-title" {
		t.Fatalf("initial title = %+v, want derived-title", got)
	}

	d.handleRenameWorkspace(client, &protocol.RenameWorkspaceMessage{
		Cmd: protocol.CmdRenameWorkspace, WorkspaceID: "workspace-1", Title: "User Renamed",
	})
	if res := expectRenameResult(t, client); !res.Success {
		t.Fatalf("rename_result success=false error=%q", protocol.Deref(res.Error))
	}

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-1", Title: "derived-title", Directory: dir,
	})
	if got := d.store.GetWorkspace("workspace-1"); got == nil || got.Title != "User Renamed" {
		t.Fatalf("stored title after re-register = %+v, want User Renamed", got)
	}
	if snap, ok := d.workspaces.snapshot("workspace-1"); !ok || snap.Title != "User Renamed" {
		t.Fatalf("registry title after re-register = %q ok=%v, want User Renamed", snap.Title, ok)
	}
}

func TestDaemon_HandleRenameSession_RejectsEmptyName(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	dir := t.TempDir()
	now := string(protocol.TimestampNow())
	addTestWorkspace(d, "workspace-s1", dir)
	d.store.Add(&protocol.Session{
		ID: "s1", Label: "original", Agent: protocol.SessionAgentClaude,
		Directory: dir, WorkspaceID: "workspace-s1",
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	client := newRenameTestClient()
	d.handleRenameSession(client, &protocol.RenameSessionMessage{
		Cmd: protocol.CmdRenameSession, SessionID: "s1", Label: "   ",
	})

	if res := expectRenameResult(t, client); res.Success {
		t.Fatal("rename with blank label should fail")
	}
	if got := d.store.Get("s1"); got == nil || got.Label != "original" {
		t.Fatalf("label after rejected rename = %+v, want original", got)
	}
}

func TestRenameSessionOverTheUnixSocketAppliesTheNameCap(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	dir := t.TempDir()
	addTestWorkspace(d, "workspace-s1", dir)
	d.store.Add(&protocol.Session{ID: "s1", Label: "original", Agent: protocol.SessionAgentClaude, Directory: dir, WorkspaceID: "workspace-s1"})

	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	responses := make(chan protocol.Response, 2)
	go func() {
		dec := json.NewDecoder(client)
		for range 2 {
			var resp protocol.Response
			if err := dec.Decode(&resp); err != nil {
				return
			}
			responses <- resp
		}
	}()

	d.handleRenameSessionConn(server, &protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: "s1", Label: strings.Repeat("x", 49)})
	if resp := <-responses; resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "over the 48-character limit") {
		t.Fatalf("over-cap rename response = %+v, want a refusal naming the limit", resp)
	}
	if got := d.store.Get("s1"); got.Label != "original" {
		t.Fatalf("label after refused rename = %q, want original", got.Label)
	}

	d.handleRenameSessionConn(server, &protocol.RenameSessionMessage{Cmd: protocol.CmdRenameSession, SessionID: "s1", Label: "  review store tripwires  "})
	if resp := <-responses; !resp.Ok {
		t.Fatalf("rename response = %+v, want ok", resp)
	}
	if got := d.store.Get("s1"); got.Label != "review store tripwires" {
		t.Fatalf("label after rename = %q, want the trimmed name", got.Label)
	}
}
