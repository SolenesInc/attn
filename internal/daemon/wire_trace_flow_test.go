package daemon

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
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
	d.ptyBackend = &fakeSpawnBackend{}
	trace := wireRecorder(d)

	workDir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("create work dir: %v", err)
	}
	client := newWorkspaceProtocolTestClient()

	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "sess-1", Cwd: workDir, Agent: protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store), Placement: &protocol.SessionPlacement{}, Label: protocol.Ptr("one"),
		Cols: 80, Rows: 24,
	})
	runDaemonSocketCommand(t, func(conn net.Conn) {
		d.handleTodos(conn, &protocol.TodosMessage{
			ID: "sess-1", Todos: []string{"write the migration"},
		})
	})
	d.handleRenameSession(client, &protocol.RenameSessionMessage{
		Cmd: protocol.CmdRenameSession, SessionID: "sess-1", Label: "renamed",
	})
	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: "sess-1"})

	assertWireGolden(t, "flow", renderWireTrace(trace, map[string]string{
		dir: "<tmp>",
	}))
}
