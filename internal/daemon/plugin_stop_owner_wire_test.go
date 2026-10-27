package daemon_test

import (
	"sync"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAStopReturningAfterReloadDoesNotRetireTheReloadedDriver(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"resume": true, "state_reporting": true})
		awaitDriverAvailable(app, "snipe")
		session, first := spawnDriven(w, app, driver, w.Path("shop"))
		term := w.terminal(session)
		release := make(chan struct{})
		releaseKill := sync.OnceFunc(func() { close(release) })
		t.Cleanup(releaseKill)
		term.OnKill(func(syscall.Signal) {
			term.Exit(143)
			<-release
		})
		app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(session)})
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
		if ended := driver.closed(); ended.RunID != first.RunID {
			t.Fatalf("stop ended run %s, want %s", ended.RunID, first.RunID)
		}
		result := testworld.Request(app, protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(session), Cols: 80, Rows: 24}, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == session })
		if !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
		second := driver.launched()
		releaseKill()
		w.advance(0)
		if err := driver.state(second, 1, "working"); err != nil {
			t.Fatalf("the old stop retired reloaded run %s: %v", second.RunID, err)
		}
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	})
}
