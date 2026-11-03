package daemon_test

import (
	"bytes"
	"encoding/base64"
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
	handled := func(p *testworld.Peer) protocol.GetScreenSnapshotResultMessage {
		return testworld.Request(p, protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: terminal},
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

	desktop.TypeLine(session, `printf 'mark%s\n' er-painted`)
	desktop.AwaitScreen(session, "marker-painted")
	painted := handled(desktop)
	screen, err := base64.StdEncoding.DecodeString(protocol.Deref(painted.ScreenSnapshot))
	if !painted.Success || err != nil || !bytes.Contains(screen, []byte("marker-painted")) {
		t.Errorf("the snapshot of a painted shell = %+v (%v), want a screen showing marker-painted", painted, err)
	}
	if protocol.Deref(painted.ScreenCols) != 100 || protocol.Deref(painted.ScreenRows) != 30 {
		t.Errorf("the painted screen is %dx%d, want the pane's spawned 100x30", protocol.Deref(painted.ScreenCols), protocol.Deref(painted.ScreenRows))
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

	phone.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: " ", Source: protocol.Ptr("response")})
	if answer := handled(phone); protocol.Deref(answer.Cols) != 120 || protocol.Deref(answer.Rows) != 36 {
		t.Fatalf("a terminal reply the phone forwarded resized the terminal to %dx%d; the desktop's 120x36 should hold", protocol.Deref(answer.Cols), protocol.Deref(answer.Rows))
	}

	mark = len(phone.Log())
	desktop.Send(protocol.DetachSessionMessage{Cmd: protocol.CmdDetachSession, ID: terminal})
	awaitResized(phone, mark, 50, 30)
	sizeInSession(phone, 50, 30)

	laptop := w.App()
	laptop.Watch(session)
	mark = len(laptop.Log())
	laptop.Send(protocol.TerminalFocusMessage{Cmd: protocol.CmdTerminalFocus, ID: terminal})
	resize(laptop, 80, 24)
	awaitResized(laptop, mark, 80, 24)

	mark = len(phone.Log())
	phone.Send(protocol.TerminalPointerActivityMessage{Cmd: protocol.CmdTerminalPointerActivity, ID: terminal})
	awaitResized(phone, mark, 50, 30)

	sizeInSession(laptop, 80, 24)

	mark = len(phone.Log())
	laptop.Close()
	awaitResized(phone, mark, 50, 30)
	sizeInSession(phone, 50, 30)
}
