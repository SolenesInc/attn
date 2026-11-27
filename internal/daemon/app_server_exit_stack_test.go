package daemon_test

import (
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAReplacementAppServerStillServesItsSessionAfterTheOldServersLateExitAndARestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma))
	starting := s.PauseEvery(pausepoint.AdapterTerminalStarting)
	exiting := s.PauseEvery(pausepoint.PtyExitAccepted)
	exited := s.PauseEvery(pausepoint.PtyExitHandled)
	s.Start()
	app := s.App()
	setSetting(t, app, "sigma_enabled", "true")
	shop := s.Path("shop")
	if err := os.MkdirAll(shop, 0o755); err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	app.Send(protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(session), Agent: string(fakeagent.Sigma), Cwd: shop,
		ProfileID: app.SelectedProfile(), Placement: &protocol.SessionPlacement{}, Cols: 100, Rows: 30,
	})
	starting.Await()
	starting.Release()
	if spawned := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return string(r.ID) == session }); !spawned.Success {
		t.Fatalf("spawn: %s", protocol.Deref(spawned.Error))
	}
	tui := s.Launched(session)
	server := s.AppServer(fakeagent.Sigma)
	conversation := tui.ConversationID
	mark := len(app.Log())
	app.Type(session, "rename the orders table\r", userKeystroke)
	tui.Prompted()
	server.Prompted(conversation)
	testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })

	var launched int
	for range 2 {
		mark = len(app.Log())
		server.Reply(conversation, "Renamed. Keep the old view? <!-- attn:state=waiting_input -->")
		testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })

		launched = s.AppServers(fakeagent.Sigma)
		server.Crash()
		starting.Await()
		exiting.Await()
		starting.Release()
		server = s.AppServerAfter(fakeagent.Sigma, launched)
		exiting.Release()
		exited.Await()
		exited.Release()

		mark = len(app.Log())
		server.Work(conversation)
		testworld.AwaitSessionSince(app, mark, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	}
	pid := server.PID()

	s.Stop()
	s.Start()
	app = s.App()
	testworld.AwaitSessionFromConnect(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	server.Reply(conversation, "Dropped it. Anything else? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })
	if now := s.AppServer(fakeagent.Sigma).PID(); now != pid || s.AppServers(fakeagent.Sigma) != launched+1 {
		t.Errorf("after the restart the server has pid %d and %d servers started, want the replacement %d kept", now, s.AppServers(fakeagent.Sigma), pid)
	}
}
