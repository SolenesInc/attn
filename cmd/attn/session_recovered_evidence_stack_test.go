package main_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAgentThatMovedOnWhileTheDaemonWasDownComesBackAsItNowIs(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app, cli := s.App(), s.Client()
	finished, finishedRun := claudeAtWork(t, s, app, "finished")
	answered, answeredRun := claudeAtWork(t, s, app, "answered")
	if err := cli.RecordNotification(answered, "permission_prompt", "Allow edit?"); err != nil {
		t.Fatalf("ask for approval: %v", err)
	}
	testworld.AwaitSession(app, answered, func(x protocol.Session) bool { return x.State == protocol.SessionStatePendingApproval })

	s.Stop()
	finishedRun.ReplyUnheard("Added the discount field. <!-- attn:state=idle -->")
	answeredRun.ReplyUnheard("Edited it. <!-- attn:state=idle -->")
	s.Start()
	app = s.App()

	came := map[string]protocol.Session{}
	for _, x := range app.Initial.Sessions {
		came[x.ID] = x
	}
	if came[answered].State == protocol.SessionStatePendingApproval {
		t.Errorf("the approval answered while the daemon was down came back pending")
	}
	for name, id := range map[string]string{"finished": finished, "answered": answered} {
		settled := came[id]
		if settled.State == protocol.SessionStateWorking || settled.State == protocol.SessionStateLaunching {
			settled = testworld.AwaitSession(app, id, func(x protocol.Session) bool {
				return x.State != protocol.SessionStateWorking && x.State != protocol.SessionStateLaunching
			})
		}
		if settled.State != protocol.SessionStateIdle {
			t.Errorf("the agent that %s while the daemon was down came back %s (%s), want idle", name, settled.State, protocol.Deref(settled.StateReason))
		}
	}
}

func TestACodexReviewerKeepsAnsweringBriefApprovalsUnseenAfterADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	requestID := uuid.NewString()
	set := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "auto_approve_enabled", Value: "true", RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(set.Success) {
		t.Fatalf("turn the reviewer on: %s", protocol.Deref(set.Error))
	}
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "run the migration")
	codex.Prompted()
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })

	s.Stop()
	s.Start()
	app = s.App()
	codex.AskApproval()
	app.AwaitScreen(session, "Allow the command to run?")
	codex.Dismiss()
	codex.Reply("Ran it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
	for _, e := range app.Received() {
		if e.Session != nil && e.Session.ID == session && e.Session.State == protocol.SessionStatePendingApproval {
			t.Fatal("after the restart the app saw an approval the reviewer answered inside its dwell")
		}
	}
}

func claudeAtWork(t *testing.T, s *testworld.Stack, app *testworld.Peer, repo string) (string, *fakeagent.Run) {
	t.Helper()
	id := s.Spawn(app, fakeagent.Claude, s.Path(repo))
	run := s.Launched(id)
	app.TypeLine(id, "add a discount field")
	run.Prompted()
	testworld.AwaitSession(app, id, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	return id, run
}
