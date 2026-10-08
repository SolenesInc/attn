package daemon_test

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestALaunchedAutomationFinishesSavingAfterDatabaseCommitsRecover(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path("check"), 0755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, cli, manualAutomation(w, "Check the code."))
	awaitAutomationChanged(app, 1)
	boot := w.HoldNextBoot()
	defer boot()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "now"})
	awaitAutomationChanged(app, 1)
	runs := automationRuns(t, cli, 1)
	if len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("runs before boot = %+v, want one pending launch", runs)
	}
	run := runs[0]
	sessionID := protocol.Deref(run.SessionID)
	testworld.AwaitSession(app, string(sessionID), func(s protocol.Session) bool { return s.ID == sessionID })
	restore := w.RefuseDatabaseCommits()
	defer restore()
	boot()
	result := testworld.Await(app, protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("now"))
	if result.Success {
		t.Fatalf("run with unavailable persistence = %+v, want the launched run still pending", result)
	}
	runs = automationRuns(t, cli, 1)
	if len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("runs after the failed commit = %+v, want the launched run still pending", runs)
	}
	agent := w.Launched(string(sessionID))
	agent.Prompted()
	if err := os.RemoveAll(w.Path("check")); err != nil {
		t.Fatal(err)
	}
	restore()
	setAutomationEnabled(t, cli, 1, false)
	runs = automationRuns(t, cli, 1)
	if len(runs) != 1 || runs[0].ID != run.ID || runs[0].State != "delivered" || protocol.Deref(runs[0].SessionID) != sessionID {
		t.Fatalf("runs after storage recovers = %+v, want the same launched run delivered", runs)
	}
	seed, err := cli.SeedShow("", protocol.Deref(run.SeedID))
	if err != nil || seed.Seed.Status != "growing" {
		t.Fatalf("launched seed = %+v (%v), want its growing state preserved", seed, err)
	}
}
