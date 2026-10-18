package daemon_test

import (
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentThatExitsIsNotReattachedWhileItsWorkerShutsDown(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
		terminal := app.Terminal(session)
		if attached := testworld.Request(app, protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: protocol.TerminalID(terminal)},
			protocol.EventAttachResult, func(r protocol.AttachResultMessage) bool { return string(r.ID) == terminal }); !attached.Success {
			t.Fatalf("attach %s refused: %s", terminal, protocol.Deref(attached.Error))
		}
		term := w.terminal(session)
		finishRemoval := term.HoldRemoval()

		go term.Exit(1)
		synctest.Wait()
		finishRemoval()
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
		synctest.Wait()

		for _, e := range app.Received() {
			if e.Event == protocol.EventPtyDesync && protocol.Deref(e.ID) == terminal {
				t.Fatalf("the exited terminal was told to reattach (%s), which wipes the exit the app shows", protocol.Deref(e.Reason))
			}
		}
	})
}
