package main

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
	"github.com/victorarias/attn/internal/protocol"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	var help bytes.Buffer
	writeSeedHelp(&help)
	out := map[string]string{"guide": seedGuideText, "help": help.String()}
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

	for _, sender := range []struct{ name, ref, replyTo string }{{"", "session:sender-id-123", "sender-i"}, {"A colleague", "session:sender-id-123", "sender-i"}, {"Keel", "member:keel", "Keel"}} {
		var b bytes.Buffer
		printAgentInbox(&b, &protocol.AgentPeerMessage{Sender: protocol.PartyView{Ref: protocol.PartyRef(sender.ref), Name: sender.name}, ReplyTo: sender.replyTo, Content: "Message λ {{literal}}\nnext"})
		out["inbox/"+sender.name] = b.String()
	}

	for _, label := range []string{"", "A colleague", "sender-i"} {
		var b bytes.Buffer
		printAgentInboxBatch(&b, &protocol.AgentInboxBatchResult{Items: []protocol.AgentInboxItem{
			{Kind: "peer_message", Content: "  Message λ {{literal}}\nnext  ", Sender: &protocol.PartyView{Ref: "session:sender-id-123", Name: label}, ReplyTo: protocol.Ptr("sender-i")},
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
