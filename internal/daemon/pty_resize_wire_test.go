package daemon_test

import (
	"fmt"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAPtyResizeIsEchoedOnceInStreamOrderKeepingTheCellSizeWhenPixelsAreUnusable(t *testing.T) {
	w := newWorld(t)
	session := w.Spawn(w.App(), workspaceShell, w.Path("shop"))
	plain := transportPeer(w)

	for i, tc := range []struct {
		name           string
		xpixel, ypixel *int
		cellSize       bool
	}{
		{name: "no pixels before any cell size is known"},
		{name: "both pixel axes", xpixel: protocol.Ptr(91 * 8), ypixel: protocol.Ptr(21 * 27), cellSize: true},
		{name: "an x axis the kernel cannot hold", xpixel: protocol.Ptr(70000), ypixel: protocol.Ptr(540), cellSize: true},
		{name: "a single axis", xpixel: protocol.Ptr(720), cellSize: true},
	} {
		cols, rows := 90+i, 20+i
		before, after := fmt.Sprintf("before-%d", i), fmt.Sprintf("after-%d", i)
		plain.TypeLine(session, fmt.Sprintf(`printf 'be%%s\n' fore-%d`, i))
		transportAwaitOutput(plain, session, before)

		plain.Send(protocol.PtyResizeMessage{Cmd: protocol.CmdPtyResize, ID: session, Cols: cols, Rows: rows, Xpixel: tc.xpixel, Ypixel: tc.ypixel})
		plain.TypeLine(session, fmt.Sprintf(`stty size; printf 'af%%s\n' ter-%d`, i))
		transportAwaitOutput(plain, session, after)

		events := plain.Received()
		var echoes []int
		for index, e := range events {
			if e.Event == protocol.EventPtyResized && protocol.Deref(e.ID) == session && protocol.Deref(e.Cols) == cols {
				echoes = append(echoes, index)
			}
		}
		if len(echoes) != 1 {
			t.Errorf("%s: the resize to %dx%d was echoed %d times, want once", tc.name, cols, rows, len(echoes))
			continue
		}
		echo := events[echoes[0]]
		if at, from, to := echoes[0], transportOutputIndex(t, events, session, before), transportOutputIndex(t, events, session, after); at < from || at > to {
			t.Errorf("%s: pty_resized arrived at %d, want it between the output before it (%d) and after it (%d)", tc.name, at, from, to)
		}
		if protocol.Deref(echo.Rows) != rows {
			t.Errorf("%s: pty_resized echoed %d rows, want %d", tc.name, protocol.Deref(echo.Rows), rows)
		}
		switch {
		case tc.cellSize && (protocol.Deref(echo.Xpixel) != cols*8 || protocol.Deref(echo.Ypixel) != rows*27):
			t.Errorf("%s: pty_resized echoed %v x %v pixels, want %d x %d from the 8x27 cell", tc.name, protocol.Deref(echo.Xpixel), protocol.Deref(echo.Ypixel), cols*8, rows*27)
		case !tc.cellSize && (echo.Xpixel != nil || echo.Ypixel != nil):
			t.Errorf("%s: pty_resized echoed %d x %d pixels, want both left off", tc.name, protocol.Deref(echo.Xpixel), protocol.Deref(echo.Ypixel))
		}
		if transportOutputIndex(t, events, session, fmt.Sprintf("%d %d", rows, cols)) < 0 {
			t.Errorf("%s: stty in the session does not report %d rows and %d cols", tc.name, rows, cols)
		}
	}
}
