package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hooks"
)

func TestADriverTakingLaunchInstructionsIsGivenTheAgentsGuidance(t *testing.T) {
	for _, reportsPullRequests := range []bool{false, true} {
		name := "a driver that reports nothing"
		if reportsPullRequests {
			name = "a driver that reports its pull requests"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			app := w.App()
			driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{
				"launch_instructions": true, "resume": true, "state_reporting": true, "pull_request_reporting": reportsPullRequests,
			})
			awaitDriverAvailable(app, "snipe")
			setSetting(t, app, "asks_enabled", "true")
			_, agent := spawnDriven(w, app, driver, w.Path("shop"))

			if got := agent.Instructions; got == nil || got.Kind != "agent" || got.ProfileID != app.SelectedProfile() {
				t.Fatalf("the agent launch carried instructions %+v, want agent guidance for profile %s", got, app.SelectedProfile())
			}
			content := agent.Instructions.Content
			for _, want := range []string{agentGuidanceLead, hooks.GardenGuidance(), askGuidanceLead} {
				if !strings.Contains(content, want) {
					t.Errorf("the agent guidance lacks %q", want)
				}
			}
			if strings.Contains(content, "shared context") {
				t.Errorf("the agent guidance still says %q", "shared context")
			}
			if told := strings.Contains(content, "attn pr record"); told == reportsPullRequests {
				t.Errorf("the agent guidance tells the agent to record its own pull requests: %v, want %v", told, !reportsPullRequests)
			}
		})
	}
}
