package main_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestLiveAgentsKeepTheirStateAndSnoozeAcrossADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app, cli := s.App(), s.Client()
	start := func(repo string) (string, *fakeagent.Run) {
		t.Helper()
		id := s.Spawn(app, fakeagent.Claude, s.Path(repo))
		run := s.Launched(id)
		app.TypeLine(id, "add a discount field")
		run.Prompted()
		testworld.AwaitSession(app, id, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
		return id, run
	}
	reply := func(id string, run *fakeagent.Run, state protocol.SessionState) {
		t.Helper()
		run.Reply("Done here. <!-- attn:state=" + string(state) + " -->")
		testworld.AwaitSession(app, id, func(x protocol.Session) bool {
			return x.State == state && protocol.Deref(x.StateReason) == "classifier_verdict"
		})
	}
	ask := func(id string) {
		t.Helper()
		if err := cli.RecordNotification(id, "permission_prompt", "Allow edit?"); err != nil {
			t.Fatalf("ask for approval: %v", err)
		}
		testworld.AwaitSession(app, id, func(x protocol.Session) bool { return x.State == protocol.SessionStatePendingApproval })
	}

	idle, idleRun := start("idle")
	reply(idle, idleRun, protocol.SessionStateIdle)
	working, _ := start("working")
	waiting, waitingRun := start("waiting")
	reply(waiting, waitingRun, protocol.SessionStateWaitingInput)
	asking, _ := start("asking")
	ask(asking)
	snoozed, snoozedRun := start("snoozed")
	reply(snoozed, snoozedRun, protocol.SessionStateIdle)
	app.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: snoozed, Until: time.Now().Add(time.Hour).Format(time.RFC3339Nano)})
	testworld.AwaitSession(app, snoozed, func(x protocol.Session) bool { return protocol.Deref(x.TurnSnoozedUntil) != "" })

	s.Stop()
	s.Start()
	app = s.App()

	want := map[string]protocol.SessionState{
		idle:    protocol.SessionStateIdle,
		working: protocol.SessionStateWorking,
		waiting: protocol.SessionStateWaitingInput,
		asking:  protocol.SessionStatePendingApproval,
		snoozed: protocol.SessionStateIdle,
	}
	for _, x := range app.Initial.Sessions {
		if w, ok := want[x.ID]; ok && x.State != w {
			t.Errorf("%s came back %s, want the %s its live agent is still in", filepath.Base(x.Directory), x.State, w)
		}
		delete(want, x.ID)
	}
	if len(want) != 0 {
		t.Errorf("sessions missing after the restart: %v", want)
	}

	app.Send(protocol.WakeTurnMessage{Cmd: protocol.CmdWakeTurn, SessionID: snoozed})
	testworld.AwaitSession(app, snoozed, func(x protocol.Session) bool {
		return protocol.Deref(x.TurnOwed) && protocol.Deref(x.TurnSnoozedUntil) == ""
	})
}
