package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

type pluginLink struct {
	daemon             *Daemon
	terminal           harness.TerminalID
	session            protocol.SessionID
	plugin, run, route string
	conversation       sessionmonitor.ConversationObservation
	registration       pluginDriverRegistration
	connection         *pluginConnection
}

type questionSafePluginLink struct{ pluginLink }

func (questionSafePluginLink) SafeDuringQuestion() {}

func (pluginLink) Voices() []harness.Voice {
	return []harness.Voice{harness.VoiceUser, harness.VoiceAttn}
}

func (l pluginLink) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	return l.request(ctx, "driver.deliver_message", in)
}

func (l pluginLink) Compact(ctx context.Context, in harness.Input) harness.Custody {
	return l.request(ctx, "driver.compact", in)
}

func (l pluginLink) compactTarget() pluginCompactTarget {
	return pluginCompactTarget{Plugin: l.plugin, Run: l.run, Conversation: l.conversation.NativeID}
}

func (l pluginLink) request(_ context.Context, method string, in harness.Input) harness.Custody {
	if in.ExpectedDriverRun != "" && (l.run != in.ExpectedDriverRun || l.conversation.NativeID != in.ExpectedConversation) {
		return harness.Custody{Rejected: true, Reason: "the input's driver run or native conversation has ended"}
	}
	if l.route != "direct" || l.connection == nil || !l.registration.Capabilities["message_delivery"] {
		return harness.Custody{Reason: fmt.Sprintf("plugin %q is unavailable for direct input to run %s", l.plugin, l.run)}
	}
	params := pluginDeliverMessageParams{TerminalID: l.terminal, RunID: l.run, InputID: in.ID, Text: in.Text}
	if l.registration.Capabilities["attributed_input"] {
		if l.conversation.NativeID == "" {
			return harness.Custody{Reason: "the driver has not reported its native conversation"}
		}
		params.ConversationID = l.conversation.NativeID
		params.Voice = "user"
		if in.Voice == harness.VoiceAttn {
			params.Voice = "attn"
		}
	}
	if method == "driver.compact" {
		if l.conversation.NativeID == "" {
			return harness.Custody{Reason: "the driver has not reported its native conversation"}
		}
		if !l.registration.Capabilities["compaction"] {
			return harness.Custody{Reason: "the plugin compaction handler is unavailable"}
		}
		params.ConversationID = l.conversation.NativeID
		if !l.daemon.compactions.bindPlugin(l.session, in.ID, l.compactTarget()) {
			return harness.Custody{Reason: "the compact request is no longer pending"}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), pluginDeliverMessageTimeout)
	defer cancel()
	var result pluginDeliverMessageResult
	if err := l.connection.request(ctx, method, params, &result); err != nil {
		return harness.Custody{Reason: fmt.Sprintf("deliver message via plugin %q: %v", l.plugin, err)}
	}
	if !result.OK {
		reason := result.Reason
		if reason == "" {
			reason = fmt.Sprintf("plugin %q declined message delivery for session %s", l.plugin, l.session)
		}
		return harness.Custody{Reason: reason, Rejected: result.Status == "rejected" || result.Status == "target_ended"}
	}
	return harness.Custody{Taken: true, At: time.Now()}
}

func (d *Daemon) linkOwnsState(sessionID protocol.SessionID) bool {
	if d.store.GetAgentDriverRun(sessionID).RunID == "" {
		return false
	}
	session := d.store.Get(sessionID)
	return session != nil && d.pluginDriverReportsState(session.Agent)
}
