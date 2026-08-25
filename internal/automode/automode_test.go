package automode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsBroadPatternMatchesTheTypeScriptRule(t *testing.T) {
	broad := []string{"*", "**", "?", " * ", "* ?", "", "\t*"}
	for _, pattern := range broad {
		if !IsBroadPattern(pattern) {
			t.Errorf("IsBroadPattern(%q) = false, want true", pattern)
		}
	}
	narrow := []string{"git push*", "rm -rf /*", "bash curl*", "a", "*.sh"}
	for _, pattern := range narrow {
		if IsBroadPattern(pattern) {
			t.Errorf("IsBroadPattern(%q) = true, want false", pattern)
		}
	}
}

func TestValidateProposalRefusesABroadAllow(t *testing.T) {
	err := ValidateProposal(KindAllow, "", "*")
	if err == nil {
		t.Fatal("a broad allow pattern was accepted")
	}
	if !strings.Contains(err.Error(), "broad allow pattern") {
		t.Fatalf("error does not name the limit: %v", err)
	}
}

func TestValidateProposalAcceptsABroadDeny(t *testing.T) {
	if err := ValidateProposal(KindDeny, "", "*"); err != nil {
		t.Fatalf("broad deny refused: %v", err)
	}
}

func TestValidateProposalChecksModelShape(t *testing.T) {
	if err := ValidateProposal(KindModel, TargetModels, "opencode-go/glm-5.3"); err != nil {
		t.Fatalf("valid model refused: %v", err)
	}
	for _, tc := range []struct{ target, value string }{
		{"", "opencode-go/glm-5.3"},
		{"main", "opencode-go/glm-5.3"},
		{TargetModels, "glm-5.3"},
	} {
		if err := ValidateProposal(KindModel, tc.target, tc.value); err == nil {
			t.Errorf("ValidateProposal(model, %q, %q) was accepted", tc.target, tc.value)
		}
	}
	// Naming nothing is how a user proposes turning auto mode off again.
	if err := ValidateProposal(KindModel, TargetModels, ""); err != nil {
		t.Errorf("an empty model list was refused: %v", err)
	}
}

func TestParseModelListReadsAnOrderedLayer(t *testing.T) {
	models, err := ParseModelList(" vendor/primary , vendor/fallback ")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 2 || models[0] != "vendor/primary" || models[1] != "vendor/fallback" {
		t.Fatalf("models = %v, want the pair in order", models)
	}
	if FormatModelList(models) != "vendor/primary,vendor/fallback" {
		t.Errorf("format = %q", FormatModelList(models))
	}
}

func TestParseModelListRefusesWhatNoLayerCouldRunOn(t *testing.T) {
	for name, value := range map[string]string{
		"not a pair":           "glm-5.3",
		"one entry not a pair": "vendor/ok,glm-5.3",
		"the same model twice": "vendor/ok,vendor/ok",
	} {
		if _, err := ParseModelList(value); err == nil {
			t.Errorf("%s (%q) was accepted", name, value)
		}
	}
	// An empty list parses to no models, which is auto mode turned off.
	for name, value := range map[string]string{"nothing named": "  ", "only separators": ",,"} {
		models, err := ParseModelList(value)
		if err != nil || len(models) != 0 {
			t.Errorf("%s (%q) = %v, %v; want no models and no error", name, value, models, err)
		}
	}
}

func TestValidateProposalTakesAModelListAndNamesTheLayer(t *testing.T) {
	if err := ValidateProposal(KindModel, TargetModels, "vendor/a,vendor/b"); err != nil {
		t.Fatalf("a two-model list was refused: %v", err)
	}
	if err := ValidateProposal(KindModel, "", "vendor/a"); err == nil {
		t.Fatal("a model proposal with no target was accepted")
	}
	if err := ValidateProposal(KindModel, TargetModels, "vendor/a,oops"); err == nil {
		t.Fatal("a list with an entry that is not a provider/id pair was accepted")
	}
}

func TestValidateProposalRejectsUnknownKinds(t *testing.T) {
	if err := ValidateProposal("promote", "", "anything"); err == nil {
		t.Fatal("unknown proposal kind was accepted")
	}
}

// The JSON here IS plugins/attn-pi/automode/config.ts's RawAutoModeConfig: a field renamed
// on one side without the other silently drops to the pi-side default.
func TestConfigMarshalsIntoThePiSideShape(t *testing.T) {
	raw, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{
		"enabled_default", "environment", "allow", "hard_deny",
		"models",
	}
	if len(fields) != len(want) {
		t.Fatalf("config has %d fields, want exactly %d: %s", len(fields), len(want), raw)
	}
	for _, name := range want {
		if _, ok := fields[name]; !ok {
			t.Errorf("config is missing %q: %s", name, raw)
		}
	}
}

func TestShippedHardDenyCoversAutoModesOwnSurfaces(t *testing.T) {
	patterns := ShippedHardDeny("29849")
	joined := strings.Join(patterns, "\n")
	if !strings.Contains(joined, "attn automode env") {
		t.Errorf("shipped hard deny does not cover `attn automode env`: %v", patterns)
	}
	for _, verb := range []string{"allow", "deny", "model", "show", "denials"} {
		if strings.Contains(joined, "attn automode "+verb) {
			t.Errorf("shipped hard deny covers %q, which does not write the config: %v", verb, patterns)
		}
	}
	if !strings.Contains(joined, "localhost:29849") {
		t.Errorf("shipped hard deny does not name the daemon's own port: %v", patterns)
	}
	for _, pattern := range patterns {
		if IsBroadPattern(pattern) {
			t.Errorf("shipped hard deny %q names nothing", pattern)
		}
	}
}

func TestShippedHardDenyWithoutAPortNamesOnlyTheCLI(t *testing.T) {
	for _, pattern := range ShippedHardDeny("") {
		if strings.Contains(pattern, ":") {
			t.Errorf("pattern %q names a port when none was given", pattern)
		}
	}
}

func TestResolveHardDenyPutsShippedFirstAndDropsDuplicates(t *testing.T) {
	shipped := ShippedHardDeny("9849")
	resolved := ResolveHardDeny("9849", []string{"ssh prod*", shipped[0], "ssh prod*"})
	if len(resolved) != len(shipped)+1 {
		t.Fatalf("resolved = %v, want the shipped set plus one stored pattern", resolved)
	}
	for i, pattern := range shipped {
		if resolved[i] != pattern {
			t.Fatalf("resolved[%d] = %q, want the shipped %q", i, resolved[i], pattern)
		}
	}
	stored := StripShippedHardDeny("9849", resolved)
	if len(stored) != 1 || stored[0] != "ssh prod*" {
		t.Errorf("stripped = %v, want only the stored pattern", stored)
	}
}
