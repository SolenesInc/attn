package guidance

import (
	"slices"
	"testing"
)

func TestRegistryValidates(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPost990CompositionKeepsStandingCopyAtLaunchAndTailInHook(t *testing.T) {
	snapshot, err := Catalog(DefaultScenario())
	if err != nil {
		t.Fatal(err)
	}
	system := compositionByID(t, snapshot, "system-prompt")
	hook := compositionByID(t, snapshot, "session-start")
	if !slices.Contains(blockIDs(system), "launch.garden") {
		t.Fatal("home launch does not carry standing garden guidance")
	}
	if slices.Contains(blockIDs(hook), "launch.garden") {
		t.Fatal("SessionStart repeats standing garden guidance")
	}
	if !slices.Contains(blockIDs(hook), "hook.garden-at-plot") {
		t.Fatal("SessionStart lost the live plot tail")
	}
}

func TestScenarioAxesSelectRoleGardenAndDelivery(t *testing.T) {
	tests := []struct {
		name       string
		scenario   Scenario
		wantUnit   string
		wantActive bool
		want       Delivery
	}{
		{"Claude plain at home", Scenario{Home: true, Agent: AgentClaude, Role: RolePlain, Garden: GardenNoPlot}, "launch.garden", true, DeliveryClaudeSystem},
		{"Codex delegate at seed", Scenario{Home: true, Agent: AgentCodex, Role: RoleDelegate, Garden: GardenAtSeed}, "delegate.seed-reporting", true, DeliveryInitial},
		{"Plugin outpost", Scenario{Agent: AgentPlugin, Role: RolePlain, Garden: GardenNoPlot}, "refusal.outpost", true, DeliveryCLIStdout},
		{"Crew member", Scenario{Home: true, Agent: AgentCodex, Role: RoleCrew, Garden: GardenAtPlot}, "launch.crew", true, DeliveryCodexSystem},
		{"Chief is not workspace role", Scenario{Home: true, Agent: AgentClaude, Role: RoleChief, Garden: GardenNoPlot, HasContext: true}, "launch.workspace-context", false, DeliveryClaudeSystem},
		{"Plugin launch capability is visible", Scenario{Home: true, Agent: AgentPlugin, Role: RolePlain, Garden: GardenNoPlot}, "launch.garden", false, DeliveryPluginSystem},
		{"Plugin nudge uses in-band delivery", Scenario{Home: true, Agent: AgentPlugin, Role: RoleCrew, Garden: GardenNoPlot, PluginMessages: true}, "nudge.heartbeat", true, DeliveryPluginMsg},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := Catalog(test.scenario)
			if err != nil {
				t.Fatal(err)
			}
			unit := unitByID(t, snapshot, test.wantUnit)
			if unit.Eligible != test.wantActive || unit.Delivery != test.want {
				t.Fatalf("%s = eligible %v delivery %q", unit.ID, unit.Eligible, unit.Delivery)
			}
		})
	}
}

func TestLaunchCompositionOrderLeavesCrewLast(t *testing.T) {
	snapshot, err := Catalog(Scenario{Home: true, Agent: AgentCodex, Role: RoleCrew, Garden: GardenNoPlot, HasContext: true, WorkflowEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"launch.chief", "launch.workspace-context", "launch.workflow", "launch.garden", "launch.crew"}
	if got := blockIDs(compositionByID(t, snapshot, "system-prompt")); !slices.Equal(got, want) {
		t.Fatalf("launch order = %v, want %v", got, want)
	}
}

func unitByID(t *testing.T, snapshot Snapshot, id string) UnitView {
	t.Helper()
	for _, unit := range snapshot.Units {
		if unit.ID == id {
			return unit
		}
	}
	t.Fatalf("no unit %s", id)
	return UnitView{}
}

func compositionByID(t *testing.T, snapshot Snapshot, id string) CompositionView {
	t.Helper()
	for _, composition := range snapshot.Compositions {
		if composition.ID == id {
			return composition
		}
	}
	t.Fatalf("no composition %s", id)
	return CompositionView{}
}

func blockIDs(composition CompositionView) []string {
	ids := make([]string, 0, len(composition.Blocks))
	for _, block := range composition.Blocks {
		ids = append(ids, block.UnitID)
	}
	return ids
}
