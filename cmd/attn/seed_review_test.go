package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestParseSeedReviewArgsAcceptsJSONAnywhere(t *testing.T) {
	positionals, jsonOutput, err := parseSeedReviewArgs([]string{"r-1", "--json", "s-1"})
	if err != nil {
		t.Fatalf("parseSeedReviewArgs: %v", err)
	}
	if !jsonOutput || len(positionals) != 2 || positionals[0] != "r-1" || positionals[1] != "s-1" {
		t.Fatalf("positionals = %v json = %v", positionals, jsonOutput)
	}
	if _, _, err := parseSeedReviewArgs([]string{"--model", "sonnet"}); err == nil {
		t.Fatal("per-review model flag was accepted")
	}
}

func TestSeedReviewPrintsOnlyApplicableActions(t *testing.T) {
	review := protocol.GardenReview{
		Run: protocol.GardenReviewRun{
			ID: "r-1", Status: "complete", CapturedAt: "2026-08-30T12:00:00Z",
			Recipe: protocol.GardenReviewRecipe{Agent: "codex", Model: "gpt-5.6-luna", Effort: protocol.Ptr("xhigh")},
		},
		Items: []protocol.GardenReviewItem{{
			SeedID: "s-1", Title: "Finished work", Status: "ready", Resolution: "unresolved",
			Actions:        []string{"handover", "park", "harvest", "wither"},
			Recommendation: protocol.Ptr("harvest"), Explanation: protocol.Ptr("Verification passed."),
		}},
	}
	var output bytes.Buffer
	fprintSeedReview(&output, review, true)
	text := output.String()
	if !strings.Contains(text, "actions\thandover, park, harvest, wither") {
		t.Fatalf("review output = %q", text)
	}
	if strings.Contains(text, "actions\tresume") {
		t.Fatalf("review printed unavailable Resume action: %q", text)
	}
}
