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

func TestASigmaAutomationWhoseClientExitsBeforeItsFirstTurnFailsWithItsScreen(t *testing.T) {
	for _, backend := range testworld.WorkerAndSharedHost {
		t.Run(string(backend), func(t *testing.T) {
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
