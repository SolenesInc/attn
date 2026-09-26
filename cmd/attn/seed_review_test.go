package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

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
