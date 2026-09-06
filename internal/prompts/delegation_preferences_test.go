package prompts

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDelegationDiscoveryIsOnDemand(t *testing.T) {
	for _, values := range []Values{
		{"garden_available": "true"},
		{"garden_available": "true", "notebook_root": "/tmp/notebook"},
		{"garden_available": "true", "crew_priming": "You are a crew member."},
	} {
		output := RenderText("session", "launch", values)
		if !strings.Contains(output, "Before using it, read the attn skill’s delegation reference") {
			t.Fatal("launch is missing the capability discovery hint")
		}
		if !strings.Contains(output, "other instructions define another delegation router") || !strings.Contains(output, "Settings > Delegation") {
			t.Fatal("launch is missing the conflicting delegation tripwire")
		}
		for _, absent := range []string{"--brief", "--preferences-revision", "Model choices", "Delegation preferences:"} {
			if strings.Contains(output, absent) {
				t.Errorf("launch contains on-demand detail %q", absent)
			}
		}
	}
}

func TestDelegationRolesIncludesAllChoicesAndRevision(t *testing.T) {
	roles := DelegationRoleTemplates()
	roles[2].Instructions = "Verify {{literal}} without expanding it"
	roles[2].Choices = append(roles[2].Choices, protocol.DelegationChoice{ID: "hard", Name: "Demanding", When: "Hard verification", Selection: protocol.DelegationSelection{Harness: "pi", Provider: "example", Model: "custom", Effort: "high"}})
	output := DelegationRolesText(protocol.DelegationRolesResult{Revision: 7, Roles: roles, Guidance: DelegationRoutingGuidance(7)})
	for _, expected := range []string{"Scout", "Design", "Build", "Ship", "Review", "Verify", "Orchestrator", "Hard verification", "example", "custom", "{{literal}}", "--preferences-revision 7"} {
		if !strings.Contains(output, expected) {
			t.Errorf("missing %q", expected)
		}
	}
	if strings.Contains(output, "both systems are active") {
		t.Fatal("roles output repeats stable routing guidance")
	}
	reference := RenderText("attn-skill", "delegation", nil)
	if !strings.Contains(reference, "lists one or more roles or an unmatched-work fallback") || !strings.Contains(reference, "both systems are active") || !strings.Contains(reference, "attn delegate roles") {
		t.Fatal("delegation reference is missing stable routing guidance")
	}
	guidance := DelegationExecutionGuidance("Build", roles[2].Instructions, roles[2].StoppingPoint)
	opening := DelegationOpeningWithGuidance("Task {{task_literal}}", guidance)
	if !strings.Contains(opening, "{{literal}}") || !strings.Contains(opening, "{{task_literal}}") || strings.Contains(opening, "Hard verification") {
		t.Fatal(opening)
	}
	empty := DelegationRolesText(protocol.DelegationRolesResult{})
	if strings.Contains(empty, "enabled") || strings.Contains(empty, "disabled") || !strings.Contains(empty, "Settings > Delegation") {
		t.Fatal(empty)
	}
}

func TestDelegationTemplatesKeepSelectionsUserOwned(t *testing.T) {
	roles := DelegationRoleTemplates()
	for _, role := range roles {
		for _, choice := range role.Choices {
			if choice.Selection != (protocol.DelegationSelection{}) {
				t.Fatalf("preset %s selects a harness or model for the user: %+v", role.ID, choice.Selection)
			}
		}
		opening := DelegationOpeningWithGuidance("Exercise the agreed behavior.", DelegationExecutionGuidance(role.Name, role.Instructions, role.StoppingPoint))
		if !strings.Contains(opening, role.Instructions) || !strings.Contains(opening, role.StoppingPoint) {
			t.Fatalf("preset %s lost guidance in the delegated opening", role.ID)
		}
	}
	roles[0].Choices[0].Selection.Model = "user-model"
	roles[0].Instructions = "User instructions"
	fresh := DelegationRoleTemplates()
	if fresh[0].Choices[0].Selection.Model != "" || fresh[0].Instructions == "User instructions" {
		t.Fatal("editing a preset changed later template reads")
	}
}
