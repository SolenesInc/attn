package daemon_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestScreenSnapshotRendersThePaneAtItsSize(t *testing.T) {
	w := newWorld(t)
	app := w.App()

	shell := w.Spawn(app, workspaceShell, w.Path("shop"))
	app.TypeLine(shell, `printf 'mark%s\n' er-painted`)
	app.AwaitScreen(shell, "marker-painted")
	painted := screenSnapshot(app, shell)
	screen, err := base64.StdEncoding.DecodeString(protocol.Deref(painted.ScreenSnapshot))
	if !painted.Success || err != nil || !bytes.Contains(screen, []byte("marker-painted")) {
		t.Errorf("the snapshot of a painted shell = %+v (%v), want a screen showing marker-painted", painted, err)
	}
	if protocol.Deref(painted.ScreenCols) != 100 || protocol.Deref(painted.ScreenRows) != 30 {
		t.Errorf("the painted screen is %dx%d, want the pane's 100x30", protocol.Deref(painted.ScreenCols), protocol.Deref(painted.ScreenRows))
	}

}

func screenSnapshot(app *testworld.Peer, session string) protocol.GetScreenSnapshotResultMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: session},
		protocol.EventGetScreenSnapshotResult, func(r protocol.GetScreenSnapshotResultMessage) bool { return r.ID == session })
}
