package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAReloadedSessionSurvivesItsReplacedWorkerStoppingLate(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	removeReplied := s.PauseAt(pausepoint.PtyRemoveReplied)
	stopped := s.PauseAt(pausepoint.PtyWorkerStopped)
	s.Start()
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("shop"))
	s.Launched(id)

	app.Send(protocol.ReloadSessionMessage{Cmd: protocol.CmdReloadSession, ID: protocol.SessionID(id), Cols: 100, Rows: 30})
	replaced := removeReplied.Await()
	reloaded := testworld.Await(app, protocol.EventReloadSessionResult, func(r protocol.ReloadSessionResultMessage) bool { return string(r.ID) == id })
	if !reloaded.Success {
		t.Fatalf("reload %s: %s", id, protocol.Deref(reloaded.Error))
	}
	agent := s.Launched(id)
	removeReplied.Release()
	if pid := stopped.Await(); pid != replaced {
		t.Fatalf("worker %d stopped, want the replaced worker %d", pid, replaced)
	}
	stopped.Release()

	s.Stop()
	s.Start()
	recovered := s.App()
	if state := restartedSession(recovered, id).State; state == protocol.SessionStateRecoverable {
		t.Fatalf("after the restart the reloaded session %s is %s: its worker's socket or registry is gone", id, state)
	}
	recovered.TypeLine(id, "still running")
	agent.Prompted()
}
