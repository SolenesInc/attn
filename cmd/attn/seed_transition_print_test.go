package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestFprintSeedShowsWhenThePullRequestWasLastChecked(t *testing.T) {
	var buf bytes.Buffer
	fprintSeed(&buf, protocol.Seed{
		ID: "s-7k3f9m", Status: "dormant",
		HarvestWhen: &protocol.SeedHarvestCondition{
			PullRequest: "github.com:victorarias/attn#118",
			URL:         "https://github.com/victorarias/attn/pull/118",
			CheckedAt:   protocol.Ptr("2026-09-12T11:42:00Z"),
		},
	})
	if !strings.Contains(buf.String(), "PR last checked") || !strings.Contains(buf.String(), "2026-09-12") {
		t.Fatalf("show hides when the pull request was last checked:\n%s", buf.String())
	}
}
