package automode

import (
	"os"
	"strings"
	"testing"
)

func TestEverySlotIsReadByARuleThatExists(t *testing.T) {
	policy := guardianPolicy(t)
	for _, slot := range Slots() {
		if len(slot.ReadBy) == 0 {
			t.Errorf("slot %s names no rule; nothing would ever look it up", slot.ID)
			continue
		}
		for _, rule := range slot.ReadBy {
			if !strings.Contains(policy, "### "+rule) {
				t.Errorf("slot %s says %q reads it, and the guardian policy has no such rule", slot.ID, rule)
			}
		}
	}
}

func TestEveryEnvironmentLookupHasSomewhereToLand(t *testing.T) {
	policy := guardianPolicy(t)
	if !strings.Contains(policy, "{{environment}}") {
		t.Fatal("the guardian policy no longer renders the environment; one side moved without the other")
	}
	if !strings.Contains(policy, "When a slot is empty") {
		t.Error("the guardian policy no longer says what an empty slot means")
	}
}

func guardianPolicy(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("../prompts/content/pi/guardian/policy.md")
	if err != nil {
		t.Fatalf("read the guardian policy: %v", err)
	}
	return string(source)
}

func TestSlotIDsAreStableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, slot := range Slots() {
		if slot.ID == "" || slot.Label == "" || slot.Unset == "" {
			t.Errorf("slot %+v is missing an id, a label or what it means unset", slot)
		}
		if seen[slot.ID] {
			t.Errorf("slot id %q appears twice", slot.ID)
		}
		seen[slot.ID] = true
		if slot.Kind != SlotList && slot.Kind != SlotChoice {
			t.Errorf("slot %s has kind %q", slot.ID, slot.Kind)
		}
		if slot.Kind == SlotChoice && len(slot.Choices) == 0 {
			t.Errorf("choice slot %s offers nothing to choose", slot.ID)
		}
	}
}
