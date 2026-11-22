package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
)

func init() {
	unconnectedHarness = func(d *Daemon, id harness.ID) harness.Harness {
		return pluginHarness{d: d, reg: pluginDriverRegistration{Agent: id}}
	}
}

type pluginConversation struct {
	daemon       *Daemon
	facts        harness.SessionFacts
	registration pluginDriverRegistration
	connection   *pluginConnection
}

func (h pluginHarness) Conversation(facts harness.SessionFacts) harness.Conversation {
	if l, ok := h.d.linkNamed(facts.Link).(*serverLink); ok {
		return l.conversation(facts)
	}
	if facts.Run == "" || facts.Route == "terminal" {
		return typedConversation{refusal: noCompaction(facts.Harness)}
	}
	reg, conn := h.d.ensurePluginRegistry().driverConnection(facts.Harness, facts.RunOwner)
	return pluginConversation{daemon: h.d, facts: facts, registration: reg, connection: conn}
}

func (c pluginConversation) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	return c.request(ctx, "driver.deliver_message", in)
}

func (c pluginConversation) Compact(ctx context.Context, in harness.Input) harness.Custody {
	return c.request(ctx, "driver.compact", in)
}

func (c pluginConversation) CanCompact() error {
	if c.facts.Route == "direct" && c.connection != nil && c.registration.Capabilities["compaction"] {
		return nil
	}
	return noCompaction(c.facts.Harness)
}

func (c pluginConversation) questionSafe(in harness.Input) bool {
	return c.connection != nil && c.registration.Capabilities["safe_during_question"] && in.AskedInTurn
}

func (c pluginConversation) compactTarget() pluginCompactTarget {
	return pluginCompactTarget{Plugin: c.facts.RunOwner, Run: c.facts.Run, Conversation: c.facts.Conversation}
}

func (c pluginConversation) request(_ context.Context, method string, in harness.Input) harness.Custody {
	facts := c.facts
	if in.PendingApproval && !c.questionSafe(in) {
		return harness.Custody{Outcome: harness.NotMine}
	}
	if facts.Route != "direct" || c.connection == nil || !c.registration.Capabilities["message_delivery"] {
		return harness.Custody{Reason: fmt.Sprintf("plugin %q is unavailable for direct input to run %s", facts.RunOwner, facts.Run)}
	}
	params := pluginDeliverMessageParams{TerminalID: facts.Terminal, RunID: facts.Run, InputID: in.ID, Text: in.Text}
	if c.registration.Capabilities["attributed_input"] {
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
		if !c.registration.Capabilities["compaction"] {
			return harness.Custody{Reason: "the plugin compaction handler is unavailable"}
		}
		params.ConversationID = facts.Conversation
		if !c.daemon.compactions.bindPlugin(facts.Session, in.ID, c.compactTarget()) {
			return harness.Custody{Reason: "the compact request is no longer pending"}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), pluginDeliverMessageTimeout)
	defer cancel()
	var result pluginDeliverMessageResult
	if err := c.connection.request(ctx, method, params, &result); err != nil {
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
