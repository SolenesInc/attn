package daemon_test

import (
	"os"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAutomationClaimsItsSeedBeforeItsWorkerStartsAndKeepsALaterPark(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	if err := os.MkdirAll(w.Path("check"), 0755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, cli, manualAutomation(w, "Check the code."))
	awaitAutomationChanged(app, 1)
	boot := w.HoldNextBoot()
	defer boot()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "startup-claim"})
	awaitAutomationChanged(app, 1)
	runs := automationRuns(t, cli, 1)
	if len(runs) != 1 {
		t.Fatalf("pending runs: %+v", runs)
	}
	sessionID, seedID := protocol.Deref(runs[0].SessionID), protocol.Deref(runs[0].SeedID)
	testworld.AwaitSession(app, string(sessionID), func(s protocol.Session) bool { return s.ID == sessionID })
	shown, err := cli.SeedShow("", seedID)
	if err != nil || !shown.Seed.Claimed || shown.Seed.Tender == nil || protocol.Deref(shown.Seed.Tender.SessionID) != sessionID {
		t.Fatalf("worker startup claim: %+v %v", shown, err)
	}
	if _, err := cli.SeedTransition(sessionID, seedID, "park", "", false, client.SeedTransitionOptions{}); err != nil {
		t.Fatal(err)
	}
	boot()
	result := testworld.Await(app, protocol.EventAutomationRunResult, automationAnswer[protocol.AutomationRunResultMessage]("startup-claim"))
	if !result.Success {
		t.Fatalf("launch completion: %+v", result)
	}
	w.Launched(string(sessionID))
	shown, err = cli.SeedShow("", seedID)
	if err != nil || shown.Seed.Status != "dormant" || shown.Seed.Claimed {
		t.Fatalf("launch replaced Park: %+v %v", shown, err)
	}
}
