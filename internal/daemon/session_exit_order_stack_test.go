package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentThatDiesMidTurnReportsItsExitBeforeItsIdle(t *testing.T) {
	w := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	observed := w.PauseAt(pausepoint.SessionExitObserved)
	w.Start()
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	run := w.Launched(session)
	run.Prompted()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	run.Exit(1)
	observed.Await()
	first := testworld.AwaitEvent(app, "the exit or the idle it causes", func(e protocol.WebSocketEvent) bool {
		return e.Event == protocol.EventSessionExited && string(protocol.Deref(e.SessionID)) == session ||
			e.Event == protocol.EventSessionStateChanged && e.Session != nil && string(e.Session.ID) == session && protocol.Deref(e.Session.StateReason) == "process_exited"
	})
	observed.Release()
	if first.Event != protocol.EventSessionExited {
		t.Fatalf("the app heard %s before session_exited", first.Event)
	}
	testworld.Await(app, protocol.EventSessionStateChanged, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && string(e.Session.ID) == session && protocol.Deref(e.Session.StateReason) == "process_exited"
	})
	t.Log("session_exited preceded the process_exited idle while its producer was paused")
}
