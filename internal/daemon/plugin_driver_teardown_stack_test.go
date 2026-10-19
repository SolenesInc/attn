package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARetiredDriverRunStaysRetiredWhileItsSessionCloses(t *testing.T) {
	s := testworld.NewStack(t)
	prepared := s.PauseAt(pausepoint.SessionTeardownPrepared)
	s.Start()
	app, cli := s.App(), s.Client()
	driver := dialDriverAt(t, s.DialUnix, "snipe-plugin")
	capabilities := map[string]bool{"state_reporting": true}
	driver.register("snipe", capabilities)
	awaitDriverAvailable(app, "snipe")
	session := s.Spawn(app, scriptedAgent, s.Path("shop"))
	run := driver.launched()
	if err := driver.state(run, 1, protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	closed := make(chan error, 1)
	go func() { closed <- s.Client().Unregister(protocol.SessionID(session)) }()
	prepared.Await()
	if err := driver.state(run, 2, protocol.StateIdle); err == nil {
		t.Fatal("a report from the retired run was accepted")
	}
	if err := driver.report("session.report_metadata", map[string]any{"session_id": run.SessionID, "run_id": run.RunID, "seq": 3, "metadata": map[string]string{"late": "report"}}); err == nil {
		t.Fatal("metadata from the retired run was accepted")
	}
	if err := cli.UpdateState(protocol.TerminalID(run.SessionID), protocol.StateWaitingInput); err != nil {
		t.Fatal(err)
	}
	driver.register("snipe", capabilities)
	for _, active := range driver.registered.ActiveRuns {
		if active.RunID == run.RunID {
			t.Fatalf("a later monitor view restored retired run %s", run.RunID)
		}
	}
	prepared.Release()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	notification := driver.closed()
	if notification.RunID != run.RunID || notification.SessionID != run.SessionID {
		t.Fatalf("close notification=%+v, want original run %s in terminal %s", notification, run.RunID, run.SessionID)
	}
}
