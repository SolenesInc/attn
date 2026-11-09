package main

import (
	"bytes"
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
	"github.com/victorarias/attn/internal/protocol"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
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
	prompttest.Equal(t, "agent-inbox", out)
}
