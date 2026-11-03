package daemon_test

import (
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func screenSnapshot(app *testworld.Peer, session string) protocol.GetScreenSnapshotResultMessage {
	app.T.Helper()
	terminal := app.Terminal(session)
	return testworld.Request(app, protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: protocol.TerminalID(terminal)},
		protocol.EventGetScreenSnapshotResult, func(r protocol.GetScreenSnapshotResultMessage) bool { return string(r.ID) == terminal })
}
