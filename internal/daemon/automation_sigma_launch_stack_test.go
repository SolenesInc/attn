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

func sigmaAutomationStack(t *testing.T, backend testworld.PTYBackend) (*testworld.Stack, *testworld.Peer) {
	t.Helper()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Sigma), testworld.OnPTYBackend(backend))
	s.Start()
	app := s.App()
	setSetting(t, app, "sigma_enabled", "true")
	s.Launched(s.Spawn(app, fakeagent.Sigma, s.Path("shop")))
	if err := os.MkdirAll(s.Path("check"), 0o755); err != nil {
		t.Fatal(err)
	}
	applyAutomation(t, s.Client(), fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report the flaky tests.
launch: {driver: sigma, model: retired/gpt-5.5}
location: {type: directory, path: %q}
`, s.Path("check")))
	awaitAutomationChanged(app, 1)
	return s, app
}

func TestASigmaAutomationWhoseClientExitsBeforeItsFirstTurnFailsWithItsScreen(t *testing.T) {
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
			s, app := sigmaAutomationStack(t, backend)
			const screen = "Error: no provider named retired"
			s.ExitAtNextBoot(1, screen)
			answered := runAutomationFromApp(app, "exits")
			if answered.Run != nil && answered.Run.State == "delivered" {
				t.Fatalf("Run Now answered %s for a Sigma that exits at boot, want it not delivered", describeRunAnswer(answered))
			}
			runs := automationRuns(t, s.Client(), 1)
			if len(runs) != 1 {
				t.Fatalf("runs = %+v, want one", runs)
			}
			failed := awaitStackRun(t, s, app, runs[0].ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
			if failure := protocol.Deref(failed.LastError); failed.State != "failed" || !strings.Contains(failure, "exited with code 1 before its first turn") || !strings.Contains(failure, screen) {
				t.Fatalf("a run whose Sigma exited at boot is %s with %q, want it failed naming the exit and the screen Sigma left", failed.State, failure)
			}
		})
	}
}

func TestASigmaAutomationStoppedBeforeItsFirstTurnAcrossARestartFailsWithItsScreen(t *testing.T) {
	s, app := sigmaAutomationStack(t, testworld.WorkerPTY)
	const screen = "Sigma is waiting for its provider login"
	s.RequireEnterAtNextBoot(screen)
	answered := runAutomationFromApp(app, "stuck")
	if !answered.Success || answered.Run == nil || answered.Run.State != "pending" {
		t.Fatalf("a run whose Sigma waits at boot answered %s, want it pending", describeRunAnswer(answered))
	}
	session := protocol.Deref(answered.Run.SessionID)
	app.AwaitScreen(string(session), screen)

	s.Stop()
	s.Start()
	app = s.App()
	if runs := automationRuns(t, s.Client(), 1); len(runs) != 1 || runs[0].State != "pending" {
		t.Fatalf("after the restart recovered its live client the runs are %+v, want the one run still pending", runs)
	}
	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: session})
	failed := awaitStackRun(t, s, app, answered.Run.ID, func(r protocol.AutomationRunSummary) bool { return r.State != "pending" })
	if failure := protocol.Deref(failed.LastError); failed.State != "failed" || !strings.Contains(failure, "before its first turn") || !strings.Contains(failure, screen) {
		t.Fatalf("after its Sigma was stopped at boot the run is %s with %q, want failed with the screen it left", failed.State, failure)
	}
}
