package main

import (
	"bytes"
	"fmt"
	"github.com/victorarias/attn/internal/prompttest"
	"github.com/victorarias/attn/internal/protocol"
	"testing"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{"guide": seedGuideText}
	for count := 0; count < 3; count++ {
		for scope := 0; scope < 3; scope++ {
			for author := 0; author < 2; author++ {
				r := protocol.SeedReadyResult{}
				if scope > 0 {
					r.Crown = &protocol.Seed{ID: "s-plot01", Title: "Plot {{literal}}"}
				}
				if scope == 2 {
					r.Crown.PlotProgress = &protocol.SeedPlotProgress{}
				}
				for i := 0; i < count; i++ {
					r.Seeds = append(r.Seeds, protocol.Seed{ID: fmt.Sprint("s-child", i), StepSlug: "first-child", Title: "Task λ"})
				}
				if count > 0 {
					r.Handoffs = []protocol.SeedNote{{SeedID: r.Seeds[0].ID, Body: " Handoff {{literal}} "}}
					if author > 0 {
						r.Handoffs[0].AuthorMember = "keeper"
					}
				}
				out[fmt.Sprintf("ready/%d/%d/%d", count, scope, author)] = seedPrimeFromReady(&r)
			}
		}
	}

	for _, label := range []string{"", "A colleague"} {
		var b bytes.Buffer
		printAgentInbox(&b, &protocol.AgentPeerMessage{SenderSessionID: "sender-id-123", SenderLabel: label, Content: "Message λ {{literal}}\nnext"})
		out["inbox/"+label] = b.String()
	}

	for _, label := range []string{"", "A colleague", "sender-i"} {
		var b bytes.Buffer
		printAgentInboxBatch(&b, &protocol.AgentInboxBatchResult{Items: []protocol.AgentInboxItem{
			{Kind: "peer_message", Content: "  Message λ {{literal}}\nnext  ", SenderSessionID: protocol.Ptr(" sender-id-123 "), SenderLabel: protocol.Ptr(label)},
			{Kind: "garden_seed", Content: " s-example moved: note "},
			{Kind: "maintenance_prompt", Content: " Maintain {{literal}}\nnext ", SourceID: protocol.Ptr("s-example")},
			{Kind: "unknown", SourceID: protocol.Ptr(" s-example ")},
		}, Remaining: 2})
		out["batch/"+label] = b.String()
	}
	var empty bytes.Buffer
	printAgentInboxBatch(&empty, &protocol.AgentInboxBatchResult{})
	out["batch-empty"] = empty.String()
	prompttest.Equal(t, "seed-guide", out)
}
