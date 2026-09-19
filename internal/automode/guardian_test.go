package automode

import "testing"

func TestGuardianPolicyValidation(t *testing.T) {
	for _, selection := range []GuardianSelection{{}, {Provider: "p", Model: "m", Effort: "high"}, {Effort: "off"}} {
		if err := ValidatePolicy(PolicyAmendment{Guardian: &selection}); err != nil {
			t.Fatal(err)
		}
	}
	for _, selection := range []GuardianSelection{{Provider: "p"}, {Model: "m"}, {Effort: "unknown"}, {Provider: "p q", Model: "m"}} {
		if err := ValidatePolicy(PolicyAmendment{Guardian: &selection}); err == nil {
			t.Fatalf("accepted %+v", selection)
		}
	}
}
