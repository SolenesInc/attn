package daemon_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const codexStuckOnAModal = "A new version of Codex is available. Press enter to continue"

func codexAutomationStack(t *testing.T) *testworld.Stack {
	t.Helper()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	applyCodexAutomation(t, s)
	return s
}

func applyCodexAutomation(t *testing.T, s *testworld.Stack) {
	t.Helper()
	if err := os.MkdirAll(s.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, s.Client(), fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Manual check
trigger: {type: manual}
prompt: Check the code.
launch: {driver: codex}
location: {type: directory, path: %q}
`, s.Path("check")))
}

func awaitStackRun(t *testing.T, s *testworld.Stack, app *testworld.Peer, runID string, done func(protocol.AutomationRunSummary) bool) protocol.AutomationRunSummary {
	t.Helper()
	for {
		for _, run := range automationRuns(t, s.Client(), 1) {
			if run.ID == runID && done(run) {
				return run
			}
		}
		awaitAutomationChanged(app, 1)
	}
}

func TestACodexAutomationStuckBeforeItsFirstTurnStaysPendingAcrossARestartUntilItStarts(t *testing.T) {
	s := codexAutomationStack(t)
	app := s.App()
	s.RequireEnterAtNextBoot(codexStuckOnAModal)
	answered := runAutomationFromApp(app, "stuck")
	if !answered.Success || answered.Run == nil || answered.Run.State != "pending" {
		t.Fatalf("a run whose Codex sits on a modal answered %s, want it pending", describeRunAnswer(answered))
	}
	session := string(protocol.Deref(answered.Run.SessionID))
	app.AwaitScreen(session, "A new version of Codex")

	s.Stop()
	s.Start()
	app = s.App()
	if runs := automationRuns(t, s.Client(), 1); len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("after the restart recovered its live worker the runs are %+v, want the one run still pending", runs)
	}

	app.TypeLine(session, "")
	agent := s.Launched(session)
	if opening := agent.Prompted(); !strings.Contains(opening, "Check the code.") {
		t.Fatalf("once past the modal the agent was prompted %q, want its opening prompt", opening)
	}
	delivered := awaitStackRun(t, s, app, answered.Run.ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	if delivered.State != "delivered" {
		t.Fatalf("after its first turn started the run is %+v, want delivered", delivered)
	}
	agent.Reply("Checked. <!-- attn:state=idle -->")
	app.TypeLine(session, "anything else?")
	if next := agent.Prompted(); next != "anything else?" {
		t.Fatalf("the next prompt was %q, want only what was typed and no second opening prompt", next)
	}
}

func TestACodexAutomationStoppedBeforeItsFirstTurnAcrossARestartFailsWithItsScreen(t *testing.T) {
	s := codexAutomationStack(t)
	app := s.App()
	s.RequireEnterAtNextBoot(codexStuckOnAModal)
	answered := runAutomationFromApp(app, "stuck")
	session := protocol.Deref(answered.Run.SessionID)
	app.AwaitScreen(string(session), "A new version of Codex")

	s.Stop()
	s.Start()
	app = s.App()
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	failed := awaitStackRun(t, s, app, answered.Run.ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	failure := protocol.Deref(failed.LastError)
	if failed.State != "failed" || !strings.Contains(failure, "before its first turn") || !strings.Contains(failure, "A new version of Codex") {
		t.Fatalf("after its Codex was stopped on the modal the run is %s with %q, want failed with the screen it left", failed.State, failure)
	}
}

func TestACodexAutomationConfirmedJustBeforeACrashIsDeliveredOnceWithoutRelaunching(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	confirmed := s.PauseAt("automation-launch-confirmed")
	s.StartCrashingAt("automation-launch-confirmed")
	applyCodexAutomation(t, s)
	app := s.App()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "crash"})
	runs := automationRuns(t, s.Client(), 1)
	for len(runs) == 0 {
		awaitAutomationChanged(app, 1)
		runs = automationRuns(t, s.Client(), 1)
	}
	session := string(protocol.Deref(runs[0].SessionID))
	agent := s.Launched(session)
	confirmed.Await()
	if opening := agent.Prompted(); !strings.Contains(opening, "Check the code.") {
		t.Fatalf("the agent was prompted %q, want its opening prompt", opening)
	}
	confirmed.Release()
	s.AwaitCrash()

	s.Start()
	app = s.App()
	agent.Reply("Checked. <!-- attn:state=idle -->")
	delivered := awaitStackRun(t, s, app, runs[0].ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	if delivered.State != "delivered" || protocol.Deref(delivered.SessionID) != protocol.SessionID(session) {
		t.Fatalf("after the crash the run is %+v, want delivered on session %s", delivered, session)
	}
	app.TypeLine(session, "anything else?")
	if next := agent.Prompted(); next != "anything else?" {
		t.Fatalf("the next prompt was %q, want only what was typed and no relaunched opening prompt", next)
	}
}
