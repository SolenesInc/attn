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

// The Guardian's policy renders the environment; a slot the policy never mentions
// would be a question nobody asks.
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

func TestDetectedValuesNeverOverwriteTheUser(t *testing.T) {
	env := NewEnvironment()
	if err := env.SetSlot("trusted_repo", []string{"github.com/acme/only-this"}); err != nil {
		t.Fatalf("set the trusted repo: %v", err)
	}
	filled := env.WithDetected(map[string][]string{
		"trusted_repo":    {"github.com/acme/detected"},
		"repo_visibility": {"public"},
		"domains":         {"grafana.acme.corp"},
	})

	if got := filled.Slots["trusted_repo"]; len(got) != 1 || got[0] != "github.com/acme/only-this" {
		t.Errorf("trusted_repo = %v, want what the user wrote", got)
	}
	if got := filled.Slots["repo_visibility"]; len(got) != 1 || got[0] != "public" {
		t.Errorf("repo_visibility = %v, want the detected value in an empty slot", got)
	}
	if got := filled.Slots["domains"]; len(got) != 0 {
		t.Errorf("domains = %v; a slot the schema does not detect must stay the user's", got)
	}
}

func TestDetectedChoiceMustBeOneOfTheSlotsChoices(t *testing.T) {
	filled := NewEnvironment().WithDetected(map[string][]string{"repo_visibility": {"internal"}})
	if got := filled.Slots["repo_visibility"]; len(got) != 0 {
		t.Errorf("repo_visibility = %v, want nothing: the slot takes private or public", got)
	}
}

func TestDetectedSlotsAreTheOnesTheSchemaMarks(t *testing.T) {
	ids := DetectedSlotIDs()
	if len(ids) == 0 {
		t.Fatal("no slot is detected; the session fills nothing on its own")
	}
	for _, id := range ids {
		slot, ok := FindSlot(id)
		if !ok || !slot.Detected {
			t.Errorf("DetectedSlotIDs named %s, which the schema does not detect", id)
		}
	}
}
