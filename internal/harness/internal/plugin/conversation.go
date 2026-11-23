package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func (h Harness) Conversation(facts harness.SessionFacts) harness.Conversation {
	if l := h.src.servers.Link(facts.Harness); l != nil && facts.Lifetime == harness.InServer {
		return l.Conversation(facts)
	}
	if facts.Run == "" || facts.Route == "terminal" {
		return harness.TypedConversation(harness.NoCompaction(facts.Harness))
	}
	reg, connected := h.src.ownedDriver(facts.Harness, facts.RunOwner)
	return conversation{src: h.src, facts: facts, reg: reg, connected: connected}
}

type conversation struct {
	src       *Source
	facts     harness.SessionFacts
	reg       harness.PluginDriver
	connected bool
}

func (c conversation) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	if in.Turn != harness.Queue {
		return harness.Custody{Outcome: harness.Rejected, Reason: "the " + string(c.facts.Harness) + " plugin driver cannot steer or interrupt a running turn"}
	}
	return c.request(ctx, "driver.deliver_message", in)
}

func (c conversation) Compact(ctx context.Context, in harness.Input) harness.Custody {
	return c.request(ctx, "driver.compact", in)
}

func (c conversation) CanCompact() error {
	if c.facts.Route == "direct" && c.connected && c.reg.Capabilities["compaction"] {
		return nil
	}
	return harness.NoCompaction(c.facts.Harness)
}

func (c conversation) questionSafe(in harness.Input) bool {
	return c.connected && c.reg.Capabilities["safe_during_question"] && in.AskedInTurn
}

func (c conversation) request(_ context.Context, method string, in harness.Input) harness.Custody {
	facts := c.facts
	if in.PendingApproval && !c.questionSafe(in) {
		return harness.Custody{Outcome: harness.NotMine}
	}
	if facts.Route != "direct" || !c.connected || !c.reg.Capabilities["message_delivery"] {
		return harness.Custody{Reason: fmt.Sprintf("plugin %q is unavailable for direct input to run %s", facts.RunOwner, facts.Run)}
	}
	params := deliverParams{TerminalID: protocol.TerminalID(facts.Terminal), RunID: facts.Run, InputID: in.ID, Text: in.Text}
	if c.reg.Capabilities["attributed_input"] {
		if facts.Conversation == "" {
			return harness.Custody{Reason: "the driver has not reported its native conversation"}
		}
		params.ConversationID = facts.Conversation
		params.Voice = "user"
		if in.Voice == harness.VoiceAttn {
			params.Voice = "attn"
		}
	}
	if method == "driver.compact" {
		if facts.Conversation == "" {
			return harness.Custody{Reason: "the driver has not reported its native conversation"}
		}
		if !c.reg.Capabilities["compaction"] {
			return harness.Custody{Reason: "the plugin compaction handler is unavailable"}
		}
		params.ConversationID = facts.Conversation
	}
	ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
	defer cancel()
	var result deliverResult
	if err := c.src.plugins.Call(ctx, facts.RunOwner, method, params, &result); err != nil {
		return harness.Custody{Reason: fmt.Sprintf("deliver message via plugin %q: %v", facts.RunOwner, err), Uncertain: true}
	}
	if !result.OK {
		reason := result.Reason
		if reason == "" {
			reason = fmt.Sprintf("plugin %q declined message delivery for session %s", facts.RunOwner, facts.Session)
		}
		outcome := harness.Deferred
		if result.Status == "rejected" || result.Status == "target_ended" {
			outcome = harness.Rejected
		}
		return harness.Custody{Outcome: outcome, Reason: reason}
	}
	return harness.Custody{Outcome: harness.Taken, At: time.Now()}
}
