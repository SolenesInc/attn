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
		if !strings.Contains(output, "Before delegating, read the delegation reference") {
			t.Fatal("launch is missing the capability discovery hint")
		}
		if !strings.Contains(output, "standing guidance in AGENTS.md") || !strings.Contains(output, "which should govern") {
			t.Fatal("launch is missing the conflicting delegation tripwire")
		}
		for _, absent := range []string{"--brief", "--preferences-revision", "Model choices", "Delegation preferences:"} {
			if strings.Contains(output, absent) {
				t.Errorf("launch contains on-demand detail %q", absent)
			}
		}
	}
}

func TestDelegationRolesIncludesMaintainedGuidanceAndChoices(t *testing.T) {
	roles := DelegationRoleTemplates()
	roles = ExpandDelegationRoles(roles)
	roles[2].Choices = append(roles[2].Choices, protocol.DelegationChoice{ID: "hard", Name: "Demanding", When: "Hard verification", Selection: protocol.DelegationSelection{Harness: "pi", Provider: "example", Model: "custom", Effort: "high"}})
	output := DelegationRolesText(protocol.DelegationRolesResult{Roles: roles, Guidance: DelegationRoutingGuidance()})
	for _, expected := range []string{"Pathfinder", "Builder", "Reviewer", "Orchestrator", "Hard verification", "example", "custom"} {
		if !strings.Contains(output, expected) {
			t.Errorf("missing %q", expected)
		}
	}
	if strings.Contains(output, "preferences-revision") {
		t.Fatal("roles output repeats stable routing guidance")
	}
	reference := RenderText("attn-skill", "delegation", nil)
	if !strings.Contains(reference, "attn delegate roles") || !strings.Contains(reference, "configured fallback") {
		t.Fatal("delegation reference is missing stable routing guidance")
	}
	guidance := DelegationExecutionGuidance("Reviewer", roles[2].Instructions, roles[2].StoppingPoint)
	opening := DelegationOpeningWithGuidance("Task {{task_literal}}", guidance)
	if !strings.Contains(opening, roles[2].Instructions) || !strings.Contains(opening, "{{task_literal}}") || strings.Contains(opening, "Hard verification") {
		t.Fatal(opening)
	}
	empty := DelegationRolesText(protocol.DelegationRolesResult{})
	if strings.Contains(empty, "enabled") || strings.Contains(empty, "disabled") || !strings.Contains(empty, "Settings > Delegation") {
		t.Fatal(empty)
	}
}

func TestDelegationGuidanceOwnsReviewAndPausesBeforeDispatch(t *testing.T) {
	roles := ExpandDelegationRoles(DelegationRoleTemplates())
	orchestrator, reviewer, pathfinder := roles[3], roles[2], roles[0]
	if !strings.Contains(orchestrator.Description, "reviewing each Builder's work") || !strings.Contains(orchestrator.Description, "verifying the integrated outcome") {
		t.Fatalf("orchestrator ownership is ambiguous: %q", orchestrator.Description)
	}
	if !strings.Contains(reviewer.Description, "explicitly requests a separate Reviewer delegation") {
		t.Fatalf("reviewer authority is ambiguous: %q", reviewer.Description)
	}
	rolesText := DelegationRolesText(protocol.DelegationRolesResult{Roles: DelegationRoleTemplates(), Guidance: DelegationRoutingGuidance()})
	for _, expected := range []string{"reviewing each Builder's work", "explicitly requests a separate Reviewer delegation", "wait for their answer"} {
		if !strings.Contains(rolesText, expected) {
			t.Errorf("composed role catalog is missing %q", expected)
		}
	}
	for _, expected := range []string{"present the pull-request boundaries and ordering", "review or adjust the plan, or dispatch", "wait for their answer", "earlier request included execution"} {
		if !strings.Contains(pathfinder.Instructions, expected) {
			t.Errorf("pathfinder checkpoint is missing %q: %s", expected, pathfinder.Instructions)
		}
	}

	planning := RenderText("attn-workflow-skill", "planning", nil)
	for _, expected := range []string{"proposed pull-request boundaries and ordering", "review or adjust the plan, or dispatch", "wait for their answer", "earlier request included execution"} {
		if !strings.Contains(planning, expected) {
			t.Errorf("planning guidance is missing %q", expected)
		}
	}
	delegation := RenderText("attn-skill", "delegation", nil)
	for _, expected := range []string{"mandatory checkpoint", "explicitly requests a separate Reviewer delegation", "remains the Orchestrator's responsibility", "must not add a Reviewer to a plan proactively"} {
		if !strings.Contains(delegation, expected) {
			t.Errorf("delegation guidance is missing %q", expected)
		}
	}
	for name, guidance := range map[string]string{"planning": planning, "delegation": delegation} {
		for _, expected := range []string{"requires coordinated or reviewed Builder work", "mixing harnesses or models", "coordinating agent and its Builders"} {
			if !strings.Contains(guidance, expected) {
				t.Errorf("%s guidance is missing the orchestration capability %q", name, expected)
			}
		}
		for _, unsupported := range []string{"stronger-model", "cheaper models"} {
			if strings.Contains(guidance, unsupported) {
				t.Errorf("%s guidance claims an unsupported model hierarchy %q", name, unsupported)
			}
		}
		if strings.Contains(guidance, "Carry forward authorization already given") {
			t.Errorf("%s guidance retains the conflicting authorization fast path", name)
		}
	}
}

func TestDelegationTemplatesKeepSelectionsUserOwned(t *testing.T) {
	roles := DelegationRoleTemplates()
	expanded := ExpandDelegationRoles(roles)
	for i, role := range roles {
		for _, choice := range role.Choices {
			if choice.Selection != (protocol.DelegationSelection{}) {
				t.Fatalf("preset %s selects a harness or model for the user: %+v", role.ID, choice.Selection)
			}
		}
		opening := DelegationOpeningWithGuidance("Exercise the agreed behavior.", DelegationExecutionGuidance(expanded[i].Name, expanded[i].Instructions, expanded[i].StoppingPoint))
		if !strings.Contains(opening, expanded[i].Instructions) || !strings.Contains(opening, expanded[i].StoppingPoint) {
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
