package main

import "testing"

func TestParseCrewSetArgs_CarriesTheModelAndTheWayBack(t *testing.T) {
	parsed, err := parseCrewSetArgs([]string{"trellis", "--model", "claude-haiku-4-5"})
	if err != nil || parsed.model == nil || *parsed.model != "claude-haiku-4-5" {
		t.Fatalf("parsed --model = %+v, %v", parsed.model, err)
	}
	if parsed.agent != nil || parsed.effort != nil || parsed.cwd != nil || parsed.awareness != nil {
		t.Errorf("naming only --model touched another field: %+v", parsed)
	}
	cleared, err := parseCrewSetArgs([]string{"trellis", "--model", ""})
	if err != nil || cleared.model == nil || *cleared.model != "" {
		t.Fatalf("an empty --model did not reach the daemon as a clear: %+v, %v", cleared.model, err)
	}
}
