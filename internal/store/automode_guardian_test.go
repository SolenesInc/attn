package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automode"
)

func TestGuardianConfigPersistsAndUnrelatedPolicyEditsPreserveIt(t *testing.T) {
	s := New()
	defer s.Close()
	want := automode.GuardianSelection{Provider: "provider", Model: "review/model", Effort: "high"}
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{Guardian: &want}, time.Now()); err != nil {
		t.Fatal(err)
	}
	policy := automode.PolicyUntrusted
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{ApprovalPolicy: &policy}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAutoModeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Guardian != want {
		t.Fatalf("guardian = %+v, want %+v", got.Guardian, want)
	}
	invalid := automode.GuardianSelection{Provider: "broken"}
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{Guardian: &invalid}, time.Now()); err == nil {
		t.Fatal("accepted incomplete selection")
	}
	got, err = s.GetAutoModeConfig()
	if err != nil || got.Guardian != want {
		t.Fatalf("invalid edit changed config: %+v, %v", got.Guardian, err)
	}
	reset := automode.GuardianSelection{}
	if _, err := s.SetAutoModePolicy(automode.PolicyAmendment{Guardian: &reset}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetAutoModeConfig()
	if err != nil || got.Guardian != reset {
		t.Fatalf("reset: %+v, %v", got.Guardian, err)
	}
}
