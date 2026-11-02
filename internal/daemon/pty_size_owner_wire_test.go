package daemon_test

import (
	"fmt"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheClientTheUserLastUsedOwnsThePtySize(t *testing.T) {
	w := newWorld(t)
	desktop := w.App()
	session := w.Spawn(desktop, shellHarness, w.Path("shop"))
	terminal := protocol.TerminalID(w.Terminal(session))
	phone := w.App()

	resize := func(p *testworld.Peer, cols, rows int) {
		p.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: terminal, Cols: cols, Rows: rows})
	}
	handled := func(p *testworld.Peer) {
		testworld.Request(p, protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: terminal},
			protocol.EventGetScreenSnapshotResult, func(r protocol.GetScreenSnapshotResultMessage) bool { return r.ID == terminal })
	}
	step := 0
	sizeInSession := func(p *testworld.Peer, cols, rows int) {
		t.Helper()
		step++
		p.TypeLine(session, fmt.Sprintf(`printf 'step%d=%%s.\n' "$(stty size)"`, step))
		p.AwaitScreen(session, fmt.Sprintf("step%d=%d %d.", step, rows, cols))
	}
	awaitResized := func(p *testworld.Peer, mark, cols, rows int) {
		t.Helper()
		testworld.AwaitSince(p, mark, protocol.EventPtyResized, func(e protocol.WebSocketEvent) bool {
			return protocol.Deref(e.ID) == string(terminal) && protocol.Deref(e.Cols) == cols && protocol.Deref(e.Rows) == rows
		})
	}

	resize(desktop, 120, 36)
	sizeInSession(desktop, 120, 36)

	phone.Watch(session)
	resize(phone, 50, 30)
	handled(phone)
	sizeInSession(desktop, 120, 36)

	sizeInSession(phone, 50, 30)

	mark := len(desktop.Log())
	desktop.Send(protocol.TerminalFocusMessage{Cmd: protocol.CmdTerminalFocus, ID: terminal})
	awaitResized(desktop, mark, 120, 36)

	mark = len(phone.Log())
	desktop.Send(protocol.DetachSessionMessage{Cmd: protocol.CmdDetachSession, ID: terminal})
	awaitResized(phone, mark, 50, 30)
	sizeInSession(phone, 50, 30)

	laptop := w.App()
	laptop.Watch(session)
	resize(laptop, 80, 24)
	mark = len(laptop.Log())
	laptop.Send(protocol.TerminalPointerActivityMessage{Cmd: protocol.CmdTerminalPointerActivity, ID: terminal})
	awaitResized(laptop, mark, 80, 24)

	mark = len(phone.Log())
	laptop.Close()
	awaitResized(phone, mark, 50, 30)
	sizeInSession(phone, 50, 30)
}
