package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func runDaemonSocketCommand(t *testing.T, fn func(conn net.Conn)) {
	t.Helper()
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(server)
		_ = server.Close()
	}()
	_, _ = io.Copy(io.Discard, client)
	_ = client.Close()
	<-done
}

func TestWireTraceFlowGolden(t *testing.T) {
	dir := t.TempDir()
	d := NewForTesting(filepath.Join(dir, "test.sock"))
	t.Cleanup(d.closeSessionReopenBroker)
	d.ptyBackend = &fakeSpawnBackend{}
	broker := d.sessionReopenBroker()
	resolve := broker.resolve
	releaseReopen := make(chan struct{})
	broker.resolve = func(ctx context.Context, key reopenKey) (sessionReopenVerdict, error) {
		select {
		case <-releaseReopen:
			return resolve(ctx, key)
		case <-ctx.Done():
			return sessionReopenVerdict{}, ctx.Err()
		}
	}
	trace := &WireTrace{}
	reopenResolved := make(chan struct{}, 1)
	d.wsHub.wireTap = func(payload []byte) {
		trace.record(payload)
		var envelope struct {
			Event string `json:"event"`
		}
		if json.Unmarshal(payload, &envelope) == nil && envelope.Event == protocol.EventSessionReopenResolved {
			reopenResolved <- struct{}{}
		}
	}

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("create workspace dir: %v", err)
	}
	client := newWorkspaceProtocolTestClient()
	d.wsHub.add(client)

	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "workspace-1", Title: "One", Directory: workspaceDir,
	})
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: "workspace-1",
		PaneID: protocol.Ptr("pane-1"), SessionID: "sess-1", Title: protocol.Ptr("one"),
	})
	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleRegister(conn, &protocol.RegisterMessage{
			ID: "sess-1", Label: protocol.Ptr("one"), Dir: workspaceDir,
			Agent: protocol.Ptr(protocol.SessionAgentClaude), WorkspaceID: "workspace-1",
		})
	})
	layout := d.store.GetWorkspaceLayout("workspace-1")
	layout.Panes[0].Status = workspacelayout.PaneStatusReady
	if err := d.store.SaveWorkspaceLayout(*layout); err != nil {
		t.Fatalf("mark registered pane ready: %v", err)
	}
	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleTodos(conn, &protocol.TodosMessage{
			ID: "sess-1", Todos: []string{"write the migration"},
		})
	})
	d.handleRenameSession(client, &protocol.RenameSessionMessage{
		Cmd: protocol.CmdRenameSession, SessionID: "sess-1", Label: "renamed",
	})
	d.handleRenameWorkspace(client, &protocol.RenameWorkspaceMessage{
		Cmd: protocol.CmdRenameWorkspace, WorkspaceID: "workspace-1", Title: "Renamed",
	})
	d.handleMuteWorkspaceWS(client, &protocol.MuteWorkspaceMessage{
		Cmd: protocol.CmdMuteWorkspace, WorkspaceID: "workspace-1",
	})
	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: "sess-1"})
	close(releaseReopen)
	select {
	case <-reopenResolved:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for session reopen resolution")
	}
	d.handleUnregisterWorkspace(client, &protocol.UnregisterWorkspaceMessage{
		Cmd: protocol.CmdUnregisterWorkspace, ID: "workspace-1",
	})

	assertWireGolden(t, "flow", renderWireTrace(trace, map[string]string{
		dir: "<tmp>",
	}))
}
