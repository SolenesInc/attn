package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/prompttest"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOnboardingCommandsReportAndPersistTheNextFirstDay(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	requireStdout(t, s.Attn("onboarding"), "full path", "Pick a harness and model")
	requireStdout(t, s.Attn("onboarding", "done"), "finished onboarding", "attn onboarding again")
	var result protocol.OnboardingResult
	s.Attn("onboarding", "--json").JSON(t, &result)
	if !result.Onboarded || result.FirstDay != protocol.FirstDayNone {
		t.Fatalf("done: %+v", result)
	}
	requireStdout(t, s.Attn("onboarding", "again"), "full path")
	s.Stop()
	s.Start()
	s.Attn("onboarding", "--json").JSON(t, &result)
	if result.Onboarded || result.FirstDay != protocol.FirstDayFull {
		t.Fatalf("replay: %+v", result)
	}
	if bad := s.Attn("onboarding", "wrong"); bad.Code != 2 {
		t.Fatalf("invalid action exited %d", bad.Code)
	}
}

func TestTheRoleInstallCommandKeepsCustomRoles(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	s.Start()
	prompttest.Equal(t, "delegation-roles-help", map[string]string{"help": s.Attn("delegate", "roles", "--help").Stdout})
	requireStdout(t, s.Attn("delegate", "roles", "add", "special", "--name", "Specialist", "--agent", "claude", "--model", "sonnet"), "revision")
	requireStdout(t, s.Attn("delegate", "roles", "install", "-m", "use Attn roles"), "installed", "attn-workflow", "pathfinder", "builder", "reviewer", "orchestrator", "prototyper", "special")
	var preferences protocol.DelegationPreferences
	s.Attn("delegate", "roles", "show", "--json").JSON(t, &preferences)
	if !preferences.WorkflowSkillEnabled || len(preferences.Roles) != 6 || preferences.Roles[0].ID != "special" {
		t.Fatalf("installed: %+v", preferences)
	}
	requireStdout(t, s.Attn("delegate", "roles", "install"), "installed")
	s.Attn("delegate", "roles", "show", "--json").JSON(t, &preferences)
	if len(preferences.Roles) != 6 {
		t.Fatalf("reinstall duplicated roles: %+v", preferences.Roles)
	}
}
