package automode

import (
	"os"
	"strings"
	"testing"
)

func TestEverySlotIsReadByARuleThatExists(t *testing.T) {
	source, err := os.ReadFile("../prompts/content/pi/rulebook.md")
	if err != nil {
		t.Fatalf("read the rulebook: %v", err)
	}
	rulebook := string(source)
	for _, slot := range Slots() {
		if len(slot.ReadBy) == 0 {
			t.Errorf("slot %s names no rule; nothing would ever look it up", slot.ID)
			continue
		}
		for _, rule := range slot.ReadBy {
			if !strings.Contains(rulebook, rule) {
				t.Errorf("slot %s says %q reads it, and the rulebook has no such rule", slot.ID, rule)
			}
		}
	}
}

func TestEveryEnvironmentLookupHasSomewhereToLand(t *testing.T) {
	source, err := os.ReadFile("../prompts/content/pi/rulebook.md")
	if err != nil {
		t.Fatalf("read the rulebook: %v", err)
	}
	lookups := 0
	for _, line := range strings.Split(string(source), "\n") {
		for _, phrase := range []string{
			"Environment lists", "Environment names", "Environment excludes", "Environment does not list",
		} {
			if strings.Contains(line, phrase) {
				lookups++
			}
		}
	}
	if lookups < len(Slots())/2 {
		t.Fatalf("found %d environment lookups in the rulebook for %d slots; one side moved without the other",
			lookups, len(Slots()))
	}
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
