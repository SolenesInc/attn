package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOnboardingCommandsReportAndPersistTheChiefsPendingPath(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	if off := s.Attn("onboarding"); off.Code != 1 {
		t.Fatalf("onboarding with crew v2 off exited %d: %s", off.Code, off.Stderr)
	}
	requireStdout(t, s.Attn("settings", "set", "crew_v2_enabled", "true"), "true")
	requireStdout(t, s.Attn("onboarding"), "takes the full path", "attn crew set chief")
	requireStdout(t, s.Attn("onboarding", "done"), "onboarding is done", "attn onboarding again")
	requireStdout(t, s.Attn("onboarding"), "has no onboarding pending")
	requireStdout(t, s.Attn("onboarding", "again"), "pending again, on the full path")
	s.Stop()
	s.Start()
	var result protocol.OnboardingResult
	s.Attn("onboarding", "--json").JSON(t, &result)
	if protocol.Deref(result.Pending) != protocol.OnboardingPathFull || result.Configured || result.Chief != "Chief" {
		t.Fatalf("after a restart: %+v", result)
	}
	if bad := s.Attn("onboarding", "wrong"); bad.Code != 2 {
		t.Fatalf("an unknown action exited %d", bad.Code)
	}
}
