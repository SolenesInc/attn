package daemon_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
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
	s.SetCodexFolderTrust(s.Path("check"), "trusted")
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

func TestACodexAutomationConfirmedJustBeforeAMachineRebootIsDeliveredWithoutAFreshOpening(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	confirmed := s.PauseAt(pausepoint.AutomationRunFinalizing)
	s.Start()
	applyCodexAutomation(t, s)
	app := s.App()
	app.Send(protocol.AutomationRunMessage{Cmd: protocol.CmdAutomationRun, DefinitionID: 1, RequestID: "reboot"})
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
	agent.Reply("Checked. <!-- attn:state=idle -->")
	s.Kill()
	s.Reboot()
	confirmed.Release()

	ready := s.StartHeldBeforeReady(confirmed)
	confirmed.Release()
	ready()
	app = s.App()
	delivered := awaitStackRun(t, s, app, runs[0].ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	if delivered.State != "delivered" || protocol.Deref(delivered.SessionID) != protocol.SessionID(session) {
		t.Fatalf("after the reboot the run is %+v, want delivered on session %s", delivered, session)
	}
	revived := s.Launched(session)
	app.TypeLine(session, "anything else?")
	if next := revived.Prompted(); next != "anything else?" {
		t.Fatalf("the next prompt was %q, want only what was typed and no fresh opening prompt", next)
	}
}

func codexReviewOnAModal(t *testing.T) (*testworld.Stack, protocol.AutomationRunSummary, string) {
	t.Helper()
	github := newAutomationGitHub(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Vars = append(s.Vars, "ATTN_MOCK_GH_URL="+github.url, "ATTN_MOCK_GH_TOKEN=test-token", "ATTN_MOCK_GH_HOST=github.test")
	s.Start()
	clone := newRepo(t, "shop")
	runGit(t, clone, "remote", "add", "origin", "https://github.test/acme/shop.git")
	head := commitFile(t, clone, "change.go", "package change\n")
	s.SetCodexFolderTrust(clone, "trusted")
	spec := strings.Replace(automationReviewSpec("review", "github_review_requested", automationReviewOverride(clone)), "launch: {driver: claude, model: sonnet}", "launch: {driver: codex}", 1)
	applyAutomation(t, s.Client(), spec)
	app := s.App()
	refreshStackPRs(t, app)
	s.RequireEnterAtNextBoot(codexStuckOnAModal)
	github.request(42, head, false)
	refreshStackPRs(t, app)
	runs := automationRuns(t, s.Client(), 1)
	for len(runs) != 1 || runs[0].State != "pending" {
		awaitAutomationChanged(app, 1)
		runs = automationRuns(t, s.Client(), 1)
	}
	session := string(protocol.Deref(runs[0].SessionID))
	app.AwaitScreen(session, "A new version of Codex")
	checkout := testworld.AwaitSession(app, session, func(protocol.Session) bool { return true })
	s.SetCodexFolderTrust(checkout.Directory, "trusted")
	return s, runs[0], session
}

func refreshStackPRs(t *testing.T, app *testworld.Peer) {
	t.Helper()
	result := testworld.Request(app, protocol.RefreshPRsMessage{Cmd: protocol.CmdRefreshPRs}, protocol.EventRefreshPRsResult,
		func(protocol.RefreshPRsResultMessage) bool { return true })
	if !result.Success {
		t.Fatalf("refresh_prs: %s", protocol.Deref(result.Error))
	}
}

func signStackOutOfGitHub(s *testworld.Stack) {
	s.Vars = append(s.Vars, "ATTN_MOCK_GH_TOKEN=")
}

func awaitCodexReviewDelivered(t *testing.T, s *testworld.Stack, app *testworld.Peer, started protocol.AutomationRunSummary, session string) {
	t.Helper()
	app.TypeLine(session, "")
	if opening := s.Launched(session).Prompted(); opening == "" {
		t.Fatal("the agent started no turn past the modal")
	}
	delivered := awaitStackRun(t, s, app, started.ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	if delivered.State != "delivered" {
		t.Fatalf("after its first turn started the run is %+v, want delivered", delivered)
	}
}

func TestTurningOffAReviewAutomationAfterItsCodexLaunchedKeepsTheRunPendingAcrossARestartWhileGitHubIsSignedOutUntilItDelivers(t *testing.T) {
	s, started, session := codexReviewOnAModal(t)
	setAutomationEnabled(t, s.Client(), 1, false)
	if runs := automationRuns(t, s.Client(), 1); len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("after turning the automation off the runs are %+v, want the launched run still pending", runs)
	}
	s.Stop()
	signStackOutOfGitHub(s)
	s.Start()
	app := s.App()
	if runs := automationRuns(t, s.Client(), 1); len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("after a restart signed out of GitHub the runs are %+v, want the launched run still pending", runs)
	}
	awaitCodexReviewDelivered(t, s, app, started, session)
}

func rewindToBeforeTheLaunchedMarker(t *testing.T, dir string) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "attn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
ALTER TABLE automation_runs DROP COLUMN launched_at;
DELETE FROM schema_migrations WHERE version IN (1793985310641958, 1793993403875462);
`); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradingWhileACodexReviewAwaitsItsFirstTurnKeepsTheRunWhileGitHubIsSignedOut(t *testing.T) {
	for _, tc := range []struct {
		name    string
		disable bool
	}{
		{"automation stays on", false},
		{"automation turned off", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, started, session := codexReviewOnAModal(t)
			s.Stop()
			signStackOutOfGitHub(s)
			rewindToBeforeTheLaunchedMarker(t, s.Dir)
			s.Start()
			app := s.App()
			if tc.disable {
				setAutomationEnabled(t, s.Client(), 1, false)
				if runs := automationRuns(t, s.Client(), 1); len(runs) != 1 || runs[0].State != "pending" {
					t.Fatalf("after turning the automation off the runs are %+v, want the launched run still pending", runs)
				}
				seed, err := s.Client().SeedShow("", protocol.Deref(started.SeedID))
				if err != nil {
					t.Fatal(err)
				}
				if seed.Seed.Status == "withered" {
					t.Fatalf("turning the automation off withered the seed of a run whose agent is running: %+v", seed)
				}
			}
			awaitCodexReviewDelivered(t, s, app, started, session)
		})
	}
}
