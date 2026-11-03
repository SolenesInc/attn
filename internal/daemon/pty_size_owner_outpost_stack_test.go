package daemon_test

import (
	"fmt"
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOldOwnersDelayedResizeLeavesTheNewOwnersOutpostSize(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t)
	imageRequested := home.PauseAt(pausepoint.KittyImageRequested)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	desktop := pair.App
	session := home.Spawn(desktop, shellHarness, outpost.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := protocol.TerminalID(home.Terminal(session))
	desktop.Watch(session)
	phone := home.App()
	phone.Watch(session)

	resize := func(p *testworld.Peer, cols, rows int) {
		p.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: terminal, Cols: cols, Rows: rows})
	}
	awaitResized := func(p *testworld.Peer, mark, cols, rows int) {
		t.Helper()
		testworld.AwaitSince(p, mark, protocol.EventPtyResized, func(e protocol.WebSocketEvent) bool {
			return protocol.Deref(e.ID) == string(terminal) && protocol.Deref(e.Cols) == cols && protocol.Deref(e.Rows) == rows
		})
	}
	snapshot := func(p *testworld.Peer) protocol.GetScreenSnapshotResultMessage {
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

	mark := len(phone.Log())
	resize(desktop, 120, 36)
	awaitResized(phone, mark, 120, 36)
	resize(phone, 50, 36)
	snapshot(phone)

	mark = len(desktop.Log())
	desktop.TypeLine(session, kittyShowImage)
	testworld.AwaitSince(desktop, mark, protocol.EventKittyPlacements, func(m protocol.KittyPlacementsMessage) bool {
		return m.ID == terminal && len(m.Placements) > 0 && m.Placements[0].ImageID == 77
	})
	desktop.Send(protocol.GetKittyImageMessage{Cmd: protocol.CmdGetKittyImage, ID: terminal, ImageID: 77})
	imageRequested.Await()
	resize(desktop, 110, 36)
	testworld.Request(desktop, protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings}, protocol.EventSettingsUpdated, func(protocol.SettingsUpdatedMessage) bool { return true })

	mark = len(phone.Log())
	phone.Send(protocol.TerminalFocusMessage{Cmd: protocol.CmdTerminalFocus, ID: terminal})
	awaitResized(phone, mark, 50, 36)

	imageRequested.Release()
	snapshot(desktop)

	phone.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: " ", Source: protocol.Ptr("user")})
	if got := snapshot(phone); protocol.Deref(got.Cols) != 50 || protocol.Deref(got.Rows) != 36 {
		t.Fatalf("after the desktop's delayed 110x36 resize and the phone's typing, the terminal is %dx%d; the phone owns the size and asked for 50x36", protocol.Deref(got.Cols), protocol.Deref(got.Rows))
	}
	sizeInSession(phone, 50, 36)
}

func TestADepartedOwnersLateResizeLeavesTheNextClientsOutpostSize(t *testing.T) {
	home := testworld.NewStack(t)
	outpost := testworld.NewStack(t)
	ownerChecked := home.PauseAt(pausepoint.PtySizeOwnerChecked)
	released := home.PauseAt(pausepoint.PtySizesReleased)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	desktop := pair.App
	session := home.Spawn(desktop, shellHarness, outpost.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Host = protocol.Ptr(pair.Host) })
	terminal := protocol.TerminalID(home.Terminal(session))
	desktop.Watch(session)

	desktop.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: terminal, Cols: 110, Rows: 36})
	ownerChecked.Await()
	desktop.Close()
	released.Await()
	released.Release()

	phone := home.App()
	phone.Watch(session)
	phone.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: terminal, Cols: 50, Rows: 36})
	testworld.Request(phone, protocol.GetSettingsMessage{Cmd: protocol.CmdGetSettings}, protocol.EventSettingsUpdated, func(protocol.SettingsUpdatedMessage) bool { return true })
	ownerChecked.Release()

	phone.Send(protocol.TerminalFocusMessage{Cmd: protocol.CmdTerminalFocus, ID: terminal})
	phone.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: " ", Source: protocol.Ptr("user")})
	got := testworld.Request(phone, protocol.GetScreenSnapshotMessage{Cmd: protocol.CmdGetScreenSnapshot, ID: terminal},
		protocol.EventGetScreenSnapshotResult, func(r protocol.GetScreenSnapshotResultMessage) bool { return r.ID == terminal })
	if protocol.Deref(got.Cols) != 50 || protocol.Deref(got.Rows) != 36 {
		t.Fatalf("after the departed desktop's late 110x36 resize and the phone's focus and typing, the terminal is %dx%d; the phone owns the size and asked for 50x36", protocol.Deref(got.Cols), protocol.Deref(got.Rows))
	}
	phone.TypeLine(session, `printf 'size=%s.\n' "$(stty size)"`)
	phone.AwaitScreen(session, "size=36 50.")
}
