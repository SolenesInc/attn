package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
)

func TestADriverTakingLaunchInstructionsIsGivenTheAgentsOrTheChiefsGuidance(t *testing.T) {
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
			_, agent := spawnDriven(w, app, driver, w.Path("shop"))
			_, chief := spawnDriven(w, app, driver, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })

			if got := agent.Instructions; got == nil || got.Kind != "agent" || got.ProfileID != app.SelectedProfile() {
				t.Fatalf("the agent launch carried instructions %+v, want agent guidance for profile %s", got, app.SelectedProfile())
			}
			if got := chief.Instructions; got == nil || got.Kind != "chief" || got.NotebookRoot == "" {
				t.Fatalf("the chief launch carried instructions %+v, want chief guidance with its notebook", got)
			}
			for _, row := range []struct {
				launch  string
				content string
				want    []string
				unwant  []string
			}{
				{launch: "agent", content: agent.Instructions.Content,
					want: []string{hooks.AgentGuidance, hooks.GardenGuidance}, unwant: []string{"shared context"}},
				{launch: "chief", content: chief.Instructions.Content,
					want: []string{"You are the chief of staff", chief.Instructions.NotebookRoot, hooks.GardenGuidance}},
			} {
				for _, want := range row.want {
					if !strings.Contains(row.content, want) {
						t.Errorf("the %s guidance lacks %q", row.launch, want)
					}
				}
				for _, unwant := range row.unwant {
					if strings.Contains(row.content, unwant) {
						t.Errorf("the %s guidance still says %q", row.launch, unwant)
					}
				}
				if told := strings.Contains(row.content, hooks.PullRequestSelfReportGuidance); told == reportsPullRequests {
					t.Errorf("the %s guidance tells the agent to record its own pull requests: %v, want %v", row.launch, told, !reportsPullRequests)
				}
			}
		})
	}
}
